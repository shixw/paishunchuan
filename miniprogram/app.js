// app.js
App({
  globalData: {
    serverUrl: '',
    isConnected: false
  },
  // 全局心跳定时器ID
  healthTimer: null,
  // 当前是否在拍照页（用于避免后台心跳）
  isOnCameraPage: false,

  onLaunch() {
    const savedUrl = wx.getStorageSync('serverUrl');
    if (savedUrl) {
      this.globalData.serverUrl = savedUrl;
    }
  },

  setServerUrl(url) {
    this.globalData.serverUrl = url;
    wx.setStorageSync('serverUrl', url);
  },

  setConnected(connected) {
    this.globalData.isConnected = connected;
  },

  // 启动全局心跳（仅在拍照页时有效）
  startGlobalHealthCheck(callback, interval = 5000) {
    this.stopGlobalHealthCheck();
    this.healthTimer = setInterval(() => {
      // 只有当前在拍照页且服务地址存在时才执行回调
      if (this.isOnCameraPage && this.globalData.serverUrl) {
        callback && callback();
      }
    }, interval);
  },

  // 停止全局心跳
  stopGlobalHealthCheck() {
    if (this.healthTimer) {
      clearInterval(this.healthTimer);
      this.healthTimer = null;
    }
  }
});