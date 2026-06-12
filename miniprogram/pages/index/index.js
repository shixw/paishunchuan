const api = require('../../utils/api.js');
const app = getApp();

Page({
  data: {
    serverUrl: '',
    healthStatus: 'checking',
    cameraAuth: false,
    tag: '默认',
    position: 'back',
    flashMode: 'off',
    flashText: '关闭',
    resolution: 'high',        // low, medium, high
    resolutionText: '高',
    showPanel: false,
    cameraReady: false
  },

  cameraContext: null,
  failCount: 0,
  maxFailures: 1,

  onLoad() {
    this.checkServerAndNavigate();
  },

  onShow() {
    app.isOnCameraPage = true;
    this.checkWifiAndProceed();
    app.startGlobalHealthCheck(() => this.checkHealthNow(), 5000);
    this.checkHealthNow();
  },

  onHide() {
    app.isOnCameraPage = false;
    app.stopGlobalHealthCheck();
  },

  onUnload() {
    app.isOnCameraPage = false;
    app.stopGlobalHealthCheck();
  },

  checkServerAndNavigate() {
    const url = app.globalData.serverUrl;
    if (!url) {
      wx.redirectTo({ url: '/pages/connect/connect' });
    } else {
      this.setData({ serverUrl: url });
    }
  },

  checkWifiAndProceed() {
    wx.getNetworkType({
      success: (res) => {
        if (res.networkType !== 'wifi') {
          wx.showModal({
            title: '未连接WiFi',
            content: '请连接WiFi后再使用拍瞬传',
            confirmText: '去连接',
            success: () => wx.redirectTo({ url: '/pages/connect/connect' })
          });
        } else {
          this.checkCameraAuth();
        }
      },
      fail: () => wx.redirectTo({ url: '/pages/connect/connect' })
    });
  },

  checkCameraAuth() {
    wx.getSetting({
      success: (res) => {
        if (res.authSetting['scope.camera']) {
          this.setData({ cameraAuth: true });
          this.cameraContext = wx.createCameraContext();
          this.setData({ cameraReady: true });
        } else {
          this.setData({ cameraAuth: false });
          this.requestCameraAuth();
        }
      }
    });
  },

  requestCameraAuth() {
    wx.authorize({
      scope: 'scope.camera',
      success: () => {
        this.setData({ cameraAuth: true });
        this.cameraContext = wx.createCameraContext();
        this.setData({ cameraReady: true });
      },
      fail: () => {
        wx.showModal({
          title: '提示',
          content: '需要相机权限，请去设置中开启',
          confirmText: '去设置',
          success: (res) => {
            if (res.confirm) wx.openSetting();
            else this.setData({ cameraAuth: false });
          }
        });
      }
    });
  },

  onCameraReady() {
    console.log('camera ready');
    this.setData({ cameraReady: true });
    if (!this.cameraContext) {
      this.cameraContext = wx.createCameraContext();
    }
  },

  onCameraError(e) {
    console.error('camera error', e);
  },

  togglePanel() {
    this.setData({ showPanel: !this.data.showPanel });
  },

  switchCamera() {
    this.setData({
      position: this.data.position === 'back' ? 'front' : 'back'
    });
  },

  toggleFlash() {
    const modes = ['off', 'on', 'auto'];
    const current = this.data.flashMode;
    let idx = modes.indexOf(current);
    let next = modes[(idx + 1) % modes.length];
    let text = next === 'off' ? '关闭' : (next === 'on' ? '开启' : '自动');
    this.setData({ flashMode: next, flashText: text });
    if (this.cameraContext) {
      this.cameraContext.setFlash({ flash: next }).catch(err => console.warn('setFlash failed', err));
    }
  },

  toggleResolution() {
    const modes = ['low', 'medium', 'high'];
    const texts = ['低', '中', '高'];
    let idx = modes.indexOf(this.data.resolution);
    let nextIdx = (idx + 1) % modes.length;
    this.setData({
      resolution: modes[nextIdx],
      resolutionText: texts[nextIdx]
    });
  },

  onTagChange(e) {
    let newTag = e.detail.value.trim();
    if (newTag === '') newTag = '默认';
    this.setData({ tag: newTag });
  },

  takePhoto() {
    if (!this.data.cameraAuth) {
      this.requestCameraAuth();
      return;
    }
    if (this.data.healthStatus !== 'online') {
      wx.showToast({ title: '服务未连接', icon: 'none' });
      return;
    }
    if (!this.cameraContext || !this.data.cameraReady) {
      wx.showToast({ title: '相机未就绪', icon: 'none' });
      return;
    }
    this.cameraContext.takePhoto({
      quality: this.data.resolution,   // 使用当前分辨率作为拍照质量
      success: (res) => {
        wx.showLoading({ title: '上传中...', mask: true });
        this.uploadImage(res.tempImagePath);
      },
      fail: (err) => {
        console.error('拍照失败', err);
        wx.showToast({ title: '拍照失败', icon: 'none' });
      }
    });
  },

  async uploadImage(filePath) {
    const url = this.data.serverUrl;
    const tag = this.data.tag || '默认';
    try {
      await api.uploadImage(filePath, url, tag);
      wx.hideLoading();
      wx.showToast({ title: '上传成功', icon: 'success' });
    } catch (err) {
      wx.hideLoading();
      wx.showToast({ title: '上传失败', icon: 'none' });
      this.checkHealthNow();
    }
  },

  async checkHealthNow() {
    try {
      const ok = await api.checkHealth(this.data.serverUrl);
      if (ok) {
        this.failCount = 0;
        if (this.data.healthStatus !== 'online') this.setData({ healthStatus: 'online' });
      } else {
        this.handleHealthFail();
      }
    } catch {
      this.handleHealthFail();
    }
  },

  handleHealthFail() {
    this.failCount++;
    if (this.failCount >= this.maxFailures && this.data.healthStatus !== 'offline') {
      this.setData({ healthStatus: 'offline' });
      wx.showModal({
        title: '服务连接断开',
        content: '是否重新连接？',
        confirmText: '重连',
        success: (res) => res.confirm && this.reconnect()
      });
    } else if (this.data.healthStatus === 'online' && this.failCount < this.maxFailures) {
      this.setData({ healthStatus: 'checking' });
    }
  },

  async reconnect() {
    const ok = await api.testConnection(this.data.serverUrl);
    if (ok) {
      this.failCount = 0;
      this.setData({ healthStatus: 'online' });
    } else {
      wx.redirectTo({ url: '/pages/connect/connect' });
    }
  },

  // 编辑标签（弹出对话框）
  editTag() {
    wx.showModal({
        title: '修改标签',
        editable: true,
        placeholderText: '请输入标签',
        content: this.data.tag,
        success: (res) => {
            if (res.confirm && res.content) {
                let newTag = res.content.trim();
                if (newTag === '') newTag = '默认';
                this.setData({ tag: newTag });
            }
        }
    });
  },

  goToConnect() {
    wx.redirectTo({ url: '/pages/connect/connect' });
  }
});