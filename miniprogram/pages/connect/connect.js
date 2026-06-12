const api = require('../../utils/api.js');
const app = getApp();

Page({
  data: {
    serverUrl: '',
    inputUrl: '',
    testing: false,
    testResult: '',
    wifiConnected: true
  },

  onLoad() {
    // 进入连接页，确保心跳停止（拍照页已经停止，但再保险一次）
    app.isOnCameraPage = false;
    app.stopGlobalHealthCheck();
    this.checkWifi();
    const saved = app.globalData.serverUrl;
    if (saved) {
      this.setData({ inputUrl: saved });
    }
  },

  onShow() {
    this.checkWifi();
  },

  onUnload() {
    // 离开时无需启动心跳，由拍照页负责
  },

  checkWifi() {
    wx.getNetworkType({
      success: (res) => {
        const isWifi = (res.networkType === 'wifi');
        this.setData({ wifiConnected: isWifi });
        if (!isWifi) {
          wx.showToast({
            title: '请连接WiFi后使用',
            icon: 'none',
            duration: 3000
          });
        }
      },
      fail: () => {
        this.setData({ wifiConnected: false });
      }
    });
  },

  onInputChange(e) {
    this.setData({ inputUrl: e.detail.value });
  },

  async testConnection() {
    let url = this.data.inputUrl.trim();
    if (!url) {
      wx.showToast({ title: '请输入服务端地址', icon: 'none' });
      return;
    }
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      url = 'http://' + url;
    }
    this.setData({ serverUrl: url, testing: true, testResult: '' });
    try {
      const ok = await api.testConnection(url);
      if (ok) {
        this.setData({ testResult: '✓ 连接成功', testing: false });
        app.setServerUrl(url);
        app.setConnected(true);
        // 连接成功后跳转到拍照页，拍照页会自己启动心跳
        wx.redirectTo({ url: '/pages/index/index' });
      } else {
        this.setData({ testResult: '✗ 连接失败，请检查地址和服务端', testing: false });
      }
    } catch (err) {
      this.setData({ testResult: '✗ 连接异常：' + err.message, testing: false });
    }
  },

  scanQRCode() {
    wx.scanCode({
      onlyFromCamera: true,
      scanType: ['qrCode'],
      success: (res) => {
        let url = res.result;
        if (!url.startsWith('http://') && !url.startsWith('https://')) {
          url = 'http://' + url;
        }
        this.setData({ inputUrl: url });
        this.testConnection();
      },
      fail: (err) => {
        if (err.errMsg !== 'scanCode:fail cancel') {
          wx.showToast({ title: '扫码失败', icon: 'none' });
        }
      }
    });
  },
  gotoHelp() {
    wx.navigateTo({ url: '/pages/help/help' });
  }
});