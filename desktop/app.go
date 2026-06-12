package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.design/x/clipboard"
)

// ---------- 数据结构 ----------
type ImageRecord struct {
	Filename     string    `json:"filename"`
	OriginalName string    `json:"originalName"`
	Size         int64     `json:"size"`
	UploadTime   time.Time `json:"uploadTime"`
	URL          string    `json:"url"`
	Tag          string    `json:"tag"`
}

type ClientInfo struct {
	IP         string    `json:"ip"`
	LastActive time.Time `json:"lastActive"`
}

type App struct {
	ctx context.Context

	httpServer *http.Server
	uploadDir  string // 图片保存目录（用户家目录下）
	httpPort   int    // 实际使用的HTTP端口

	images      []ImageRecord
	imagesMutex sync.RWMutex

	clients      map[string]ClientInfo
	clientsMutex sync.RWMutex

	autoCopy bool // 是否自动复制到剪贴板
}

// ---------- 构造函数 ----------
func NewApp() *App {
	return &App{
		clients: make(map[string]ClientInfo),
	}
}

// 查找可用端口
func findAvailablePort(startPort int) int {
	for port := startPort; port < startPort+100; port++ {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			ln.Close()
			return port
		}
	}
	return 0
}

// ---------- Wails 生命周期 ----------
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	clipboard.Init()

	// 使用用户家目录作为图片存储根目录（保证可写）
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("获取用户家目录失败:", err)
	}
	a.uploadDir = filepath.Join(home, "拍瞬传图片")
	log.Printf("图片保存目录: %s", a.uploadDir)

	if err := os.MkdirAll(a.uploadDir, 0755); err != nil {
		log.Fatal("无法创建图片保存目录:", err)
	}

	// 查找可用端口
	port := findAvailablePort(5000)
	if port == 0 {
		log.Fatal("无法找到可用端口")
	}
	a.httpPort = port
	log.Printf("HTTP 服务将使用端口: %d", a.httpPort)

	go a.startHTTPServer()
	go a.cleanOldClients()
}

// 导出给前端的端口获取方法
func (a *App) GetHTTPPort() int {
	return a.httpPort
}

func (a *App) domReady(ctx context.Context) {
	// 不需要额外操作
}

// ---------- HTTP 服务 ----------
func (a *App) startHTTPServer() {
	mux := http.NewServeMux()

	mux.HandleFunc("/upload", a.handleUpload)
	mux.HandleFunc("/health", a.handleHealth)
	mux.HandleFunc("/api/images", a.handleImages)
	mux.HandleFunc("/api/clients", a.handleClients)
	mux.HandleFunc("/api/image", a.handleDeleteImage)
	mux.HandleFunc("/api/copy-image", a.handleCopyImage)
	mux.HandleFunc("/api/setting", a.handleSetting)
	mux.HandleFunc("/qrcode", a.handleQRCode)
	mux.HandleFunc("/open-dir", a.handleOpenDir)

	// 静态文件服务（嵌入或外部目录，只读）
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// 图片静态文件服务（从 uploadDir 提供）
	mux.Handle("/received_images/", http.StripPrefix("/received_images/", http.FileServer(http.Dir(a.uploadDir))))

	// 状态页重定向
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})

	a.httpServer = &http.Server{
		Addr:    ":" + strconv.Itoa(a.httpPort),
		Handler: corsMiddleware(mux),
	}
	log.Printf("HTTP server listening on :%d", a.httpPort)
	if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("HTTP server error: %v", err)
	}
}

// CORS 中间件（允许 Wails 前端跨域）
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- 业务处理 ----------

// 处理图片复制请求（前端主动复制）
func (a *App) handleCopyImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	imagePath := r.FormValue("path")
	if imagePath == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}

	// 解码并验证路径
	decodedPath, err := url.PathUnescape(imagePath)
	if err != nil {
		http.Error(w, "invalid path encoding", http.StatusBadRequest)
		return
	}
	cleanPath := filepath.Clean(decodedPath)
	// 去掉 /received_images/ 前缀
	relPath := strings.TrimPrefix(cleanPath, "/received_images/")
	if relPath == cleanPath {
		http.Error(w, "invalid path prefix", http.StatusBadRequest)
		return
	}
	absPath := filepath.Join(a.uploadDir, relPath)
	// 安全检查：确保路径在 uploadDir 内
	if !strings.HasPrefix(absPath, a.uploadDir+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}

	// 读取图片并转换为PNG写入剪贴板
	data, err := os.ReadFile(absPath)
	if err != nil {
		log.Printf("读取图片失败: %v", err)
		http.Error(w, "read failed", http.StatusInternalServerError)
		return
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		log.Printf("JPEG解码失败: %v", err)
		http.Error(w, "decode failed", http.StatusInternalServerError)
		return
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		log.Printf("PNG编码失败: %v", err)
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	if err := clipboard.Write(clipboard.FmtImage, pngBuf.Bytes()); err != nil {
		log.Printf("写入剪贴板返回 (可能无关错误): %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// 设置接口（自动复制开关）
func (a *App) handleSetting(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		AutoCopy bool `json:"autoCopy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	a.autoCopy = req.AutoCopy
	log.Printf("自动复制设置已更新: %v", a.autoCopy)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	a.recordClientActivity(r)

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("photo")
	if err != nil {
		http.Error(w, "未找到字段 photo", http.StatusBadRequest)
		return
	}
	defer file.Close()

	tag := r.FormValue("tag")
	if tag == "" {
		tag = "默认"
	}
	tag = filepath.Clean(tag)
	if tag == "." || tag == ".." {
		tag = "默认"
	}

	tagDir := filepath.Join(a.uploadDir, tag)
	if err := os.MkdirAll(tagDir, 0755); err != nil {
		log.Printf("创建标签目录失败: %v", err)
		http.Error(w, "创建标签目录失败", http.StatusInternalServerError)
		return
	}

	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".jpg"
	}
	newName := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	savePath := filepath.Join(tagDir, newName)

	out, err := os.Create(savePath)
	if err != nil {
		http.Error(w, "保存文件失败", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	size, err := io.Copy(out, file)
	if err != nil {
		http.Error(w, "写入文件失败", http.StatusInternalServerError)
		return
	}

	record := ImageRecord{
		Filename:     newName,
		OriginalName: header.Filename,
		Size:         size,
		UploadTime:   time.Now(),
		URL:          fmt.Sprintf("/received_images/%s/%s", tag, newName),
		Tag:          tag,
	}

	a.imagesMutex.Lock()
	a.images = append([]ImageRecord{record}, a.images...)
	if len(a.images) > 50 {
		a.images = a.images[:50]
	}
	a.imagesMutex.Unlock()

	log.Printf("收到图片: tag=%s, %s (原: %s, 大小: %d 字节)", tag, newName, header.Filename, size)

	// 自动复制到剪贴板（如果开启）
	if a.autoCopy {
		// 重新打开文件（或使用已保存的数据）
		imgFile, err := os.Open(savePath)
		if err == nil {
			img, err := jpeg.Decode(imgFile)
			imgFile.Close()
			if err == nil {
				var pngBuf bytes.Buffer
				if err := png.Encode(&pngBuf, img); err == nil {
					clipboard.Write(clipboard.FmtImage, pngBuf.Bytes())
					log.Printf("图片已转换为PNG并复制到剪贴板")
				} else {
					log.Printf("PNG编码失败: %v", err)
				}
			} else {
				log.Printf("JPEG解码失败: %v", err)
			}
		} else {
			log.Printf("打开文件失败: %v", err)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"success": true, "filename": "%s", "tag": "%s"}`, newName, tag)

	wailsRuntime.EventsEmit(a.ctx, "imageUploaded", nil)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	a.recordClientActivity(r)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (a *App) handleImages(w http.ResponseWriter, r *http.Request) {
	a.imagesMutex.RLock()
	defer a.imagesMutex.RUnlock()
	groups := make(map[string][]ImageRecord)
	for _, img := range a.images {
		groups[img.Tag] = append(groups[img.Tag], img)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(groups)
}

func (a *App) handleClients(w http.ResponseWriter, r *http.Request) {
	a.clientsMutex.RLock()
	defer a.clientsMutex.RUnlock()
	clientsList := make([]ClientInfo, 0, len(a.clients))
	for _, c := range a.clients {
		clientsList = append(clientsList, c)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(clientsList)
}

func (a *App) handleDeleteImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		http.Error(w, "invalid path encoding", http.StatusBadRequest)
		return
	}
	cleanPath := filepath.Clean(decodedPath)
	// 去掉 /received_images/ 前缀
	relPath := strings.TrimPrefix(cleanPath, "/received_images/")
	if relPath == cleanPath {
		http.Error(w, "invalid path prefix", http.StatusBadRequest)
		return
	}
	absPath := filepath.Join(a.uploadDir, relPath)
	// 安全检查
	if !strings.HasPrefix(absPath, a.uploadDir+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	if err := os.Remove(absPath); err != nil {
		log.Printf("删除文件失败: %v", err)
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	a.imagesMutex.Lock()
	defer a.imagesMutex.Unlock()
	for i, img := range a.images {
		if img.URL == path || img.URL == decodedPath {
			a.images = append(a.images[:i], a.images[i+1:]...)
			break
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"success":true}`))
}

func (a *App) handleQRCode(w http.ResponseWriter, r *http.Request) {
	localIP := getLocalIP()
	log.Printf("IP地址: %v", localIP)
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	serverURL := fmt.Sprintf("http://%s:%d", localIP, a.httpPort)
	png, err := qrcode.Encode(serverURL, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "generate failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

func (a *App) handleOpenDir(w http.ResponseWriter, r *http.Request) {
	// 直接打开 uploadDir
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", a.uploadDir)
	case "darwin":
		cmd = exec.Command("open", a.uploadDir)
	case "linux":
		cmd = exec.Command("xdg-open", a.uploadDir)
	default:
		http.Error(w, "unsupported OS", http.StatusNotImplemented)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("打开目录失败: %v", err)
		http.Error(w, "failed to open", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// ---------- 客户端跟踪 ----------
func (a *App) recordClientActivity(r *http.Request) {
	ip := r.RemoteAddr
	host, _, _ := net.SplitHostPort(ip)
	if host == "" {
		host = ip
	}
	a.clientsMutex.Lock()
	defer a.clientsMutex.Unlock()
	_, exists := a.clients[host]
	a.clients[host] = ClientInfo{IP: host, LastActive: time.Now()}
	if !exists {
		log.Printf("新客户端连接: %s", host)
		wailsRuntime.EventsEmit(a.ctx, "clientActive", host)
	}
}

func (a *App) cleanOldClients() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		a.clientsMutex.Lock()
		now := time.Now()
		removed := make([]string, 0)
		for ip, info := range a.clients {
			if now.Sub(info.LastActive) > 30*time.Second {
				delete(a.clients, ip)
				removed = append(removed, ip)
			}
		}
		a.clientsMutex.Unlock()
		for _, ip := range removed {
			log.Printf("客户端不活跃: %s", ip)
			wailsRuntime.EventsEmit(a.ctx, "clientInactive", ip)
		}
	}
}

// ---------- 辅助函数 ----------
func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return ""
}
