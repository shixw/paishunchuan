const api = require('../../utils/api.js');
const app = getApp();

Page({
  data: {
    inputUrl: '',
    inputDisabled: false,
    ipError: false,
    wifiConnected: true,
    testing: false,
    testResult: '',
    testSuccess: false,
    version: 'v1.0.0'
  },

  onLoad() {
    // 获取版本号（正式版自动获取，非正式版用默认值）
    try {
      const accountInfo = wx.getAccountInfoSync();
      const ver = accountInfo.miniProgram.version;
      if (ver) {
        this.setData({ version: 'v' + ver });
      }
    } catch (e) {
      // 降级使用默认版本
    }
    this.checkWifi();
    const saved = app.globalData.serverUrl;
    if (saved) {
      this.setData({ inputUrl: saved });
    }
  },

  onShow() {
    this.checkWifi();
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
    this.setData({ 
      inputUrl: e.detail.value,
      ipError: false
    });
  },

  onInputFocus() {
    this.setData({ ipError: false });
  },

  onInputBlur(e) {
    const value = e.detail.value;
    if (value && !this.validateIP(value)) {
      this.setData({ ipError: true });
    } else {
      this.setData({ ipError: false });
    }
  },

  validateIP(url) {
    const pattern = /^(https?:\/\/)?((\d{1,3}\.){3}\d{1,3}):(\d{1,5})$/;
    return pattern.test(url);
  },

  pasteFromClipboard() {
    wx.getClipboardData({
      success: (res) => {
        const text = res.data.trim();
        if (text) {
          this.setData({ inputUrl: text, ipError: false });
          wx.showToast({ title: '已粘贴', icon: 'none', duration: 1500 });
        } else {
          wx.showToast({ title: '剪贴板为空', icon: 'none' });
        }
      },
      fail: () => {
        wx.showToast({ title: '读取剪贴板失败', icon: 'none' });
      }
    });
  },

  clearInput() {
    this.setData({ inputUrl: '', ipError: false });
    wx.showToast({ title: '已清空', icon: 'none', duration: 1500 });
  },

  showIPHelp() {
    wx.showModal({
      title: '获取服务端地址',
      content: '1. 电脑上打开「拍瞬传」桌面端程序\n2. 服务启动后自动显示二维码和IP地址\n3. 复制页面上的局域网IP填入此处',
      showCancel: false,
      confirmText: '我知道了'
    });
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
    if (!this.validateIP(url)) {
      this.setData({ ipError: true });
      wx.showToast({ title: 'IP或端口格式不正确', icon: 'none' });
      return;
    }
    if (!this.data.wifiConnected) {
      wx.showToast({ title: '请先连接WiFi', icon: 'none' });
      return;
    }

    this.setData({ testing: true, testResult: '', testSuccess: false });
    wx.showLoading({ title: '连接中...', mask: true });

    try {
      const ok = await api.testConnection(url);
      wx.hideLoading();
      if (ok) {
        this.setData({ testResult: '✓ 连接成功', testSuccess: true, testing: false });
        wx.showToast({ title: '连接成功', icon: 'success' });
        wx.setStorageSync('serverUrl', url);
        app.setServerUrl(url);
        app.setConnected(true);
        setTimeout(() => {
          wx.redirectTo({ url: '/pages/index/index' });
        }, 1500);
      } else {
        this.setData({ testResult: '✗ 连接失败，请检查地址和服务端', testSuccess: false, testing: false });
        wx.showModal({
          title: '❌ 连接失败',
          content: '请排查以下问题：\n1. 电脑端服务程序是否已启动？\n2. 手机与电脑是否连接同一WiFi？\n3. 防火墙是否允许该端口？',
          confirmText: '查看教程',
          cancelText: '重试',
          success: (res) => {
            if (res.confirm) {
              this.gotoHelp();
            }
          }
        });
        wx.showToast({ title: '连接失败，请检查网络', icon: 'none', duration: 3000 });
      }
    } catch (err) {
      wx.hideLoading();
      this.setData({ testResult: '✗ 连接异常：' + err.message, testSuccess: false, testing: false });
      wx.showModal({
        title: '连接异常',
        content: err.message || '网络请求失败，请检查地址是否正确',
        showCancel: false,
        confirmText: '我知道了'
      });
    }
  },

  scanQRCode() {
    if (!this.data.wifiConnected) {
      wx.showToast({ title: '请先连接WiFi', icon: 'none' });
      return;
    }

    wx.getSetting({
      success: (res) => {
        if (!res.authSetting['scope.camera']) {
          wx.authorize({
            scope: 'scope.camera',
            fail: () => {
              wx.showModal({
                title: '需要相机权限',
                content: '请允许使用相机扫码连接服务端',
                confirmText: '去设置',
                success: (modalRes) => {
                  if (modalRes.confirm) wx.openSetting();
                }
              });
            }
          });
          return;
        }
        this.startScan();
      }
    });
  },

  startScan() {
    wx.scanCode({
      onlyFromCamera: true,
      scanType: ['qrCode'],
      success: (res) => {
        let url = res.result;
        if (!url.startsWith('http://') && !url.startsWith('https://')) {
          url = 'http://' + url;
        }
        this.setData({ inputUrl: url, ipError: false });
        wx.showToast({ title: '✅ 扫码成功', icon: 'none', duration: 1500 });
        this.testConnection();
      },
      fail: (err) => {
        if (err.errMsg !== 'scanCode:fail cancel') {
          wx.showToast({ title: '扫码无效，请确认二维码正确', icon: 'none', duration: 3000 });
        }
      }
    });
  },

  gotoHelp() {
    wx.navigateTo({ url: '/pages/help/help' });
  }
});