Page({
  data: {
    githubUrl: 'https://github.com/shixw/paishunchuan/releases/latest',  // 替换为实际GitHub地址
    baiduUrl: 'https://pan.baidu.com/s/xxxxxx',  // 替换为百度网盘分享链接
    baiduCode: '提取码'  // 替换为实际提取码
  },
  copyUrl(url, successMsg = '下载链接已复制') {
    wx.setClipboardData({
      data: url,
      success: () => {
        wx.showToast({ title: successMsg, icon: 'success' });
      }
    });
  },
  copyGitHub() {
    this.copyUrl(this.data.githubUrl);
  },
  copyBaidu() {
    this.copyUrl(this.data.baiduUrl, '百度网盘链接已复制');
  }
});