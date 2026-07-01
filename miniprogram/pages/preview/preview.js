const app = getApp();

Page({
  data: {
    selectedImages: [],
    tag: '默认',
    compress: false,
    totalSize: 0,
    compressedSize: 0,
    uploading: false,
    uploadedCount: 0,
    uploadProgress: 0,
    uploadFailed: [],
    serverUrl: '',
    statusBarHeight: 20
  },

  onLoad(options) {
    // 使用 wx.getWindowInfo() 获取状态栏高度（替代废弃的 wx.getSystemInfoSync）
    try {
      const windowInfo = wx.getWindowInfo();
      this.setData({
        statusBarHeight: windowInfo.statusBarHeight || 20
      });
    } catch (e) {
      // 容错处理，使用默认值
      this.setData({ statusBarHeight: 20 });
    }

    if (options.tag) {
      this.setData({ tag: decodeURIComponent(options.tag) });
    }
    this.setData({ serverUrl: app.globalData.serverUrl });
    if (!this.data.serverUrl) {
      wx.showToast({ title: '请先连接服务端', icon: 'none' });
      setTimeout(() => {
        wx.navigateBack();
      }, 1500);
    }
  },

  // 添加更多图片
  addMore() {
    if (this.data.uploading) return;
    const remaining = 9 - this.data.selectedImages.length;
    if (remaining <= 0) {
      wx.showToast({ title: '最多选择9张', icon: 'none' });
      return;
    }
    wx.chooseMedia({
      count: remaining,
      mediaType: ['image'],
      sourceType: ['album'],
      success: (res) => {
        const newImages = res.tempFiles.map(file => ({
          tempFilePath: file.tempFilePath,
          size: file.size
        }));
        this.updateImages([...this.data.selectedImages, ...newImages]);
      },
      fail: (err) => {
        if (err.errMsg.includes('cancel')) return;
        wx.showToast({ title: '选择失败', icon: 'none' });
      }
    });
  },

  // 更新图片列表并计算大小
  updateImages(images) {
    const totalSize = images.reduce((sum, img) => sum + img.size, 0) / (1024 * 1024);
    const compressedSize = this.data.compress ? totalSize * 0.35 : totalSize;
    this.setData({
      selectedImages: images,
      totalSize: totalSize.toFixed(1),
      compressedSize: compressedSize.toFixed(1)
    });
  },

  // 移除单张图片
  removeImage(e) {
    const index = e.currentTarget.dataset.index;
    const images = this.data.selectedImages;
    images.splice(index, 1);
    this.updateImages(images);
  },

  // 预览大图
  previewImage(e) {
    const index = e.currentTarget.dataset.index;
    const urls = this.data.selectedImages.map(img => img.tempFilePath);
    wx.previewImage({
      current: urls[index],
      urls: urls
    });
  },

  // 返回
  goBack() {
    if (this.data.uploading) {
      wx.showModal({
        title: '提示',
        content: '上传进行中，确定退出吗？',
        success: (res) => {
          if (res.confirm) {
            this.cancelUpload();
            wx.navigateBack();
          }
        }
      });
    } else {
      wx.navigateBack();
    }
  },

  // 标签输入
  onTagChange(e) {
    let val = e.detail.value.trim();
    if (val === '') val = '默认';
    this.setData({ tag: val });
  },

  // 压缩选项切换
  setQuality(e) {
    const compress = e.currentTarget.dataset.compress === 'true';
    this.setData({ compress });
    const total = parseFloat(this.data.totalSize) || 0;
    const compressed = compress ? total * 0.35 : total;
    this.setData({ compressedSize: compressed.toFixed(1) });
  },

  // 开始上传
  startUpload() {
    if (this.data.selectedImages.length === 0) return;
    if (this.data.uploading) return;
    if (!this.data.serverUrl) {
      wx.showToast({ title: '服务端未连接', icon: 'none' });
      return;
    }
    this.setData({
      uploading: true,
      uploadedCount: 0,
      uploadProgress: 0,
      uploadFailed: []
    });
    this.uploadNext(0);
  },

  // 递归上传
  uploadNext(index) {
    if (index >= this.data.selectedImages.length) {
      this.onUploadComplete();
      return;
    }
    const images = this.data.selectedImages;
    const filePath = images[index].tempFilePath;
    const tag = this.data.tag || '默认';
    const url = this.data.serverUrl.replace(/\/$/, '') + '/upload';

    const doCompress = this.data.compress;
    const processFile = (path) => {
      wx.uploadFile({
        url: url,
        filePath: path,
        name: 'photo',
        formData: { tag: tag },
        success: (res) => {
          if (res.statusCode === 200) {
            const data = JSON.parse(res.data);
            if (data.success) {
              this.onUploadSuccess(index);
            } else {
              this.onUploadFail(index, data.error || '上传失败');
            }
          } else {
            this.onUploadFail(index, 'HTTP ' + res.statusCode);
          }
        },
        fail: (err) => {
          this.onUploadFail(index, err.errMsg || '网络错误');
        }
      });
    };

    if (doCompress) {
      wx.compressImage({
        src: filePath,
        quality: 80,
        success: (res) => {
          processFile(res.tempFilePath);
        },
        fail: (err) => {
          // 压缩失败，用原图
          console.warn('压缩失败，使用原图', err);
          processFile(filePath);
        }
      });
    } else {
      processFile(filePath);
    }
  },

  onUploadSuccess(index) {
    const uploaded = this.data.uploadedCount + 1;
    const progress = Math.floor((uploaded / this.data.selectedImages.length) * 100);
    this.setData({
      uploadedCount: uploaded,
      uploadProgress: progress
    });
    this.uploadNext(index + 1);
  },

  onUploadFail(index, error) {
    const failed = this.data.uploadFailed;
    failed.push({ index, error });
    this.setData({ uploadFailed: failed });
    this.uploadNext(index + 1);
  },

  onUploadComplete() {
    const total = this.data.selectedImages.length;
    const success = total - this.data.uploadFailed.length;
    const failed = this.data.uploadFailed;
    this.setData({ uploading: false });

    if (failed.length === 0) {
      wx.showToast({
        title: `✅ 上传完成 (${total}张)`,
        icon: 'none',
        duration: 2000
      });
      setTimeout(() => {
        wx.navigateBack();
      }, 2000);
    } else {
      wx.showModal({
        title: '上传完成',
        content: `成功 ${success} 张，失败 ${failed.length} 张`,
        confirmText: '重试失败',
        cancelText: '忽略',
        success: (res) => {
          if (res.confirm) {
            this.retryFailed();
          } else {
            wx.navigateBack();
          }
        }
      });
    }
  },

  retryFailed() {
    const failed = this.data.uploadFailed;
    if (failed.length === 0) {
      wx.navigateBack();
      return;
    }
    wx.showModal({
      title: '提示',
      content: '请重新选择未上传的图片',
      showCancel: false,
      success: () => {
        wx.navigateBack();
      }
    });
  },

  cancelUpload() {
    this.setData({
      uploading: false,
      uploadProgress: 0
    });
  }
});