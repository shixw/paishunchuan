/**
 * 测试服务端连通性（请求 /health 接口）
 * @param {string} baseUrl 服务端基础地址，例如 http://192.168.1.100:5000
 * @returns {Promise<boolean>}
 */
function testConnection(baseUrl) {
  return new Promise((resolve, reject) => {
    const url = `${baseUrl.replace(/\/$/, '')}/health`;
    wx.request({
      url: url,
      method: 'GET',
      timeout: 3000,
      success: (res) => {
        if (res.statusCode === 200) {
          resolve(true);
        } else {
          resolve(false);
        }
      },
      fail: (err) => {
        resolve(false);
      }
    });
  });
}

/**
 * 健康检查（轻量级，用于定时轮询）
 * @param {string} baseUrl
 * @returns {Promise<boolean>}
 */
function checkHealth(baseUrl) {
  // 复用 testConnection，但可简化
  return testConnection(baseUrl);
}

/**
 * 上传图片
 * @param {string} filePath
 * @param {string} baseUrl
 * @returns {Promise}
 */
function uploadImage(filePath, baseUrl, tag = '默认') {
  return new Promise((resolve, reject) => {
    if (!baseUrl) {
      reject(new Error('服务端地址未设置'));
      return;
    }
    const url = `${baseUrl.replace(/\/$/, '')}/upload`;
    wx.uploadFile({
      url: url,
      filePath: filePath,
      name: 'photo',
      formData: { tag: tag },   // 传递标签
      success: (res) => {
        if (res.statusCode === 200) {
          try {
            const data = JSON.parse(res.data);
            if (data.success) {
              resolve(data);
            } else {
              reject(new Error(data.error || '上传失败'));
            }
          } catch (e) {
            reject(new Error('解析响应失败'));
          }
        } else {
          reject(new Error(`HTTP ${res.statusCode}`));
        }
      },
      fail: reject
    });
  });
}

module.exports = {
  testConnection,
  checkHealth,
  uploadImage
};