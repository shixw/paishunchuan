const api = require('../../utils/api.js');
const app = getApp();

Page({
  data: {
    serverUrl: '',
    healthStatus: 'checking',  // checking, online, offline
    cameraAuth: false,
    tag: '默认'
  },

  failCount: 0,
  maxFailures: 1,        // 改为1，一次失败即断开

  onLoad() {
    this.checkServerAndNavigate();
  },

  onShow() {
    app.isOnCameraPage = true;
    this.checkWifiAndProceed();
    // 启动全局心跳
    app.startGlobalHealthCheck(() => {
      this.checkHealthNow();
    }, 5000);
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

  // 检查服务端地址是否存在
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
            success: () => {
              wx.redirectTo({ url: '/pages/connect/connect' });
            }
          });
        } else {
          this.checkCameraAuth();
        }
      },
      fail: () => {
        wx.redirectTo({ url: '/pages/connect/connect' });
      }
    });
  },

  checkCameraAuth() {
    wx.getSetting({
      success: (res) => {
        if (res.authSetting['scope.camera']) {
          this.setData({ cameraAuth: true });
        } else {
          this.setData({ cameraAuth: false });
        }
      }
    });
  },

  requestCameraAuth() {
    wx.authorize({
      scope: 'scope.camera',
      success: () => {
        this.setData({ cameraAuth: true });
      },
      fail: () => {
        wx.showModal({
          title: '提示',
          content: '需要相机权限才能拍照，请去设置中开启',
          confirmText: '去设置',
          success: (res) => {
            if (res.confirm) {
              wx.openSetting();
            } else {
              this.setData({ cameraAuth: false });
            }
          }
        });
      }
    });
  },

  // 标签输入变化
  onTagChange(e) {
    let newTag = e.detail.value.trim();
    if (newTag === '') newTag = '默认';
    this.setData({ tag: newTag });
  },

  // 拍照并上传
  takePhoto() {
    if (!this.data.cameraAuth) {
      this.requestCameraAuth();
      return;
    }
    if (this.data.healthStatus !== 'online') {
      wx.showToast({ title: '服务未连接，请检查网络', icon: 'none' });
      return;
    }
    const ctx = wx.createCameraContext();
    ctx.takePhoto({
      quality: 'high',
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
      // 上传失败可能服务端已断，主动触发一次健康检查
      this.checkHealthNow();
    }
  },

  // 健康检查（单次）
  async checkHealthNow() {
    const url = this.data.serverUrl;
    if (!url) return;
    try {
      const ok = await api.checkHealth(url);
      if (ok) {
        // 成功：重置失败计数，状态设为 online
        this.failCount = 0;
        if (this.data.healthStatus !== 'online') {
          this.setData({ healthStatus: 'online' });
          wx.showToast({ title: '服务已恢复', icon: 'success', duration: 1500 });
        }
      } else {
        // 失败处理
        this.handleHealthFail();
      }
    } catch (err) {
      // 请求异常也视为失败
      this.handleHealthFail();
    }
  },

  // 处理健康检查失败
  handleHealthFail() {
    this.failCount++;
    // 只要失败一次且当前状态不是 offline，就切换为 offline 并弹窗
    if (this.failCount >= this.maxFailures && this.data.healthStatus !== 'offline') {
      this.setData({ healthStatus: 'offline' });
      wx.showModal({
        title: '服务连接断开',
        content: '服务端无响应，是否重新连接？',
        confirmText: '重新连接',
        cancelText: '稍后',
        success: (res) => {
          if (res.confirm) {
            this.reconnect();
          }
        }
      });
    } else if (this.data.healthStatus === 'online' && this.failCount < this.maxFailures) {
      // 尚未达到阈值，但当前是在线状态，先改为 checking
      this.setData({ healthStatus: 'checking' });
    }
    // 注意：如果当前已经是 offline 或 checking，且失败次数未达阈值，不再重复改状态
  },

  // 重连逻辑
  async reconnect() {
    const url = this.data.serverUrl;
    if (!url) {
      wx.redirectTo({ url: '/pages/connect/connect' });
      return;
    }
    wx.showLoading({ title: '重连中...' });
    try {
      const ok = await api.testConnection(url);
      wx.hideLoading();
      if (ok) {
        this.failCount = 0;
        this.setData({ healthStatus: 'online' });
        wx.showToast({ title: '重连成功', icon: 'success' });
      } else {
        wx.showModal({
          title: '重连失败',
          content: '请检查服务端，或重新扫码配置',
          confirmText: '重新配置',
          success: () => {
            wx.redirectTo({ url: '/pages/connect/connect' });
          }
        });
      }
    } catch (err) {
      wx.hideLoading();
      wx.showModal({
        title: '重连异常',
        content: err.message,
        confirmText: '重新配置',
        success: () => {
          wx.redirectTo({ url: '/pages/connect/connect' });
        }
      });
    }
  },

  // 返回连接页
  goToConnect() {
    wx.redirectTo({ url: '/pages/connect/connect' });
  }
});