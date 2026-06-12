Page({
  data: {
    githubUrl: 'https://github.com/shixw/paishunchuan/releases/latest',  // 替换为实际GitHub地址
    baiduUrl: 'https://pan.baidu.com/s/1wlCR9NL4ixBJV-YuRuthtw',  // 替换为百度网盘分享链接
    baiduCode: 'ehcb'  // 替换为实际提取码
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
    let fullUrl = this.data.baiduUrl;
    if (this.data.baiduCode && !fullUrl.includes('pwd=')) {
      fullUrl += (fullUrl.includes('?') ? '&' : '?') + 'pwd=' + encodeURIComponent(this.data.baiduCode);
    }
    this.copyUrl(fullUrl, '百度网盘链接(含提取码)已复制');
  }
});