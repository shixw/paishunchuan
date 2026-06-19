package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
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

	"github.com/signintech/gopdf"

	"github.com/google/uuid"
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

	logFilePath string

	pdfDir string // PDF输出目录

	udpConn    *net.UDPConn
	udpPort    int
	udpReady   bool   // UDP 是否启动成功
	deviceID   string // 设备唯一ID (UUID)
	deviceName string // 设备友好名称
	configPath string // 配置文件路径
}

const UDP_DISCOVERY_PORT = 19988

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

	configDir := filepath.Join(home, ".paishunchuan")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		log.Printf("创建配置目录失败: %v", err)
	}
	a.configPath = filepath.Join(configDir, "config.json")
	a.loadOrGenerateDeviceInfo()

	a.uploadDir = filepath.Join(home, "拍瞬传图片")
	log.Printf("图片保存目录: %s", a.uploadDir)

	if err := os.MkdirAll(a.uploadDir, 0755); err != nil {
		log.Fatal("无法创建图片保存目录:", err)
	}

	a.logFilePath = filepath.Join(home, "拍瞬传日志.log")

	// 启动时删除旧的日志文件（避免堆积）
	if err := os.Remove(a.logFilePath); err != nil && !os.IsNotExist(err) {
		log.Printf("删除旧日志文件失败: %v", err)
	}

	logFile, err := os.OpenFile(a.logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Printf("无法创建日志文件: %v", err)
	} else {
		log.SetOutput(logFile)
		log.Println("===== 应用启动 =====")
	}

	// 初始化PDF输出目录（用户家目录下的“拍瞬传PDF”）
	if err == nil {
		a.pdfDir = filepath.Join(home, "拍瞬传PDF")
		if err := os.MkdirAll(a.pdfDir, 0755); err != nil {
			log.Printf("创建PDF目录失败: %v", err)
		}
	}

	// 查找可用端口
	port := findAvailablePort(5000)
	if port == 0 {
		log.Fatal("无法找到可用端口")
	}
	a.httpPort = port
	log.Printf("HTTP 服务将使用端口: %d", a.httpPort)

	// 4. 启动UDP发现服务（固定端口9988，失败不影响主流程）
	a.udpPort = UDP_DISCOVERY_PORT
	go a.startUDPDiscovery()

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
	mux.HandleFunc("/open-log", a.handleOpenLog)
	mux.HandleFunc("/api/pdf", a.handleGeneratePDF)
	mux.HandleFunc("/api/device-info", a.handleDeviceInfo)

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

func (a *App) handleGeneratePDF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}
	if len(req.Paths) == 0 {
		http.Error(w, "No images selected", http.StatusBadRequest)
		return
	}

	// 生成PDF
	outputPath, err := a.generatePDF(req.Paths)
	if err != nil {
		log.Printf("PDF生成失败: %v", err)
		http.Error(w, "PDF generation failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 打开目录（异步）
	go func() {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "windows":
			cmd = exec.Command("explorer", "/select,", outputPath)
		case "darwin":
			cmd = exec.Command("open", "-R", outputPath)
		case "linux":
			cmd = exec.Command("xdg-open", filepath.Dir(outputPath))
		}
		if cmd != nil {
			if err := cmd.Start(); err != nil {
				log.Printf("打开目录失败: %v", err)
			}
		}
	}()

	// 返回PDF文件流
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(outputPath)))
	http.ServeFile(w, r, outputPath)
}

func (a *App) handleOpenLog(w http.ResponseWriter, r *http.Request) {
	if a.logFilePath == "" {
		http.Error(w, "日志文件路径未设置", http.StatusInternalServerError)
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// Windows 使用 explorer 并选中文件
		cmd = exec.Command("explorer", "/select,", a.logFilePath)
	case "darwin":
		cmd = exec.Command("open", "-R", a.logFilePath)
	case "linux":
		cmd = exec.Command("xdg-open", filepath.Dir(a.logFilePath))
	default:
		http.Error(w, "unsupported OS", http.StatusNotImplemented)
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("打开日志文件失败: %v", err)
		http.Error(w, "failed to open log", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// handleCopyImage 处理图片复制请求（前端主动复制）
// handleCopyImage 处理图片复制请求
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

	decodedPath, err := url.PathUnescape(imagePath)
	if err != nil {
		http.Error(w, "invalid path encoding", http.StatusBadRequest)
		return
	}

	// 统一转换为正斜杠处理
	normalized := filepath.ToSlash(decodedPath)
	cleanPath := filepath.Clean(normalized)
	cleanPath = filepath.ToSlash(cleanPath)

	// 提取 relative path
	var relPath string
	if strings.HasPrefix(cleanPath, "/received_images/") {
		relPath = strings.TrimPrefix(cleanPath, "/received_images/")
	} else if strings.HasPrefix(cleanPath, "received_images/") {
		relPath = strings.TrimPrefix(cleanPath, "received_images/")
	} else {
		// 尝试查找 received_images 后的部分
		parts := strings.Split(cleanPath, "/")
		for i, part := range parts {
			if part == "received_images" && i+1 < len(parts) {
				relPath = strings.Join(parts[i+1:], "/")
				break
			}
		}
		if relPath == "" {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
	}

	absPath := filepath.Join(a.uploadDir, filepath.FromSlash(relPath))
	if !strings.HasPrefix(absPath, a.uploadDir+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		log.Printf("复制图片：读取文件失败 %v", err)
		http.Error(w, "read failed", http.StatusInternalServerError)
		return
	}

	// JPEG -> PNG 转换
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		log.Printf("复制图片：JPEG解码失败 %v", err)
		http.Error(w, "decode failed", http.StatusInternalServerError)
		return
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		log.Printf("复制图片：PNG编码失败 %v", err)
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}

	if err := clipboard.Write(clipboard.FmtImage, pngBuf.Bytes()); err != nil {
		log.Printf("复制图片：写入剪贴板错误 %v", err)
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

// handleDeleteImage 处理图片删除请求
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
	// 统一转换为正斜杠处理
	normalized := filepath.ToSlash(decodedPath)
	cleanPath := filepath.Clean(normalized)
	cleanPath = filepath.ToSlash(cleanPath)

	// 提取相对路径
	var relPath string
	if strings.HasPrefix(cleanPath, "/received_images/") {
		relPath = strings.TrimPrefix(cleanPath, "/received_images/")
	} else if strings.HasPrefix(cleanPath, "received_images/") {
		relPath = strings.TrimPrefix(cleanPath, "received_images/")
	} else {
		parts := strings.Split(cleanPath, "/")
		for i, part := range parts {
			if part == "received_images" && i+1 < len(parts) {
				relPath = strings.Join(parts[i+1:], "/")
				break
			}
		}
		if relPath == "" {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
	}

	absPath := filepath.Join(a.uploadDir, filepath.FromSlash(relPath))
	if !strings.HasPrefix(absPath, a.uploadDir+string(os.PathSeparator)) {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}

	if err := os.Remove(absPath); err != nil {
		log.Printf("删除图片失败: %v", err)
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	// 从内存中移除记录
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

func (a *App) handleDeviceInfo(w http.ResponseWriter, r *http.Request) {
	resp := struct {
		DeviceID   string `json:"deviceID"`
		DeviceName string `json:"deviceName"`
		UDPReady   bool   `json:"udpReady"`
		UDPError   string `json:"udpError,omitempty"`
	}{
		DeviceID:   a.deviceID,
		DeviceName: a.deviceName,
		UDPReady:   a.udpReady,
	}
	if !a.udpReady {
		resp.UDPError = "UDP发现端口被占用，设备自动发现不可用"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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

func (a *App) loadOrGenerateDeviceInfo() {
	type Config struct {
		DeviceID   string `json:"deviceID"`
		DeviceName string `json:"deviceName"`
	}
	var cfg Config

	data, err := os.ReadFile(a.configPath)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err == nil && cfg.DeviceID != "" {
			a.deviceID = cfg.DeviceID
			a.deviceName = cfg.DeviceName
			log.Printf("加载设备信息: ID=%s, Name=%s", a.deviceID, a.deviceName)
			return
		}
	}

	// 生成新的设备ID和名称
	a.deviceID = uuid.New().String() // 需要导入 "github.com/google/uuid"
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "拍瞬传设备"
	}
	a.deviceName = hostname

	// 保存配置
	cfg = Config{DeviceID: a.deviceID, DeviceName: a.deviceName}
	data, _ = json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(a.configPath, data, 0644); err != nil {
		log.Printf("保存设备配置失败: %v", err)
	}
	log.Printf("生成新设备信息: ID=%s, Name=%s", a.deviceID, a.deviceName)
}

func (a *App) startUDPDiscovery() {
	addr := &net.UDPAddr{
		IP:   net.IPv4(0, 0, 0, 0),
		Port: a.udpPort,
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Printf("⚠️ UDP发现服务启动失败 (端口 %d 被占用): %v", a.udpPort, err)
		a.udpReady = false
		wailsRuntime.EventsEmit(a.ctx, "udpError", "UDP发现端口被占用，设备自动发现功能不可用")
		return
	}
	a.udpConn = conn
	a.udpReady = true
	log.Printf("✅ UDP发现服务已启动，监听端口: %d，本机IP: %s", a.udpPort, getLocalIP())

	buffer := make([]byte, 1024)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			log.Printf("UDP读取错误: %v", err)
			continue
		}
		msg := string(buffer[:n])
		log.Printf("UDP收到消息: 长度=%d, 内容='%s', 来自 %s", n, msg, remoteAddr.String())

		if msg == "PAISHUNCHUAN_DISCOVER" {
			localIP := getLocalIP()
			if localIP == "" {
				localIP = "127.0.0.1"
			}
			deviceInfo := struct {
				DeviceID   string `json:"deviceID"`
				DeviceName string `json:"deviceName"`
				IP         string `json:"ip"`
				HTTPPort   int    `json:"httpPort"`
			}{
				DeviceID:   a.deviceID,
				DeviceName: a.deviceName,
				IP:         localIP,
				HTTPPort:   a.httpPort,
			}
			data, _ := json.Marshal(deviceInfo)
			conn.WriteToUDP(data, remoteAddr)
			log.Printf("✅ UDP回复设备信息: %s -> %s", localIP, remoteAddr.String())
		} else {
			log.Printf("UDP消息不匹配查询字符串，忽略")
		}
	}
}

// ---------- 辅助函数 ----------
func (a *App) generatePDF(relPaths []string) (string, error) {
	log.Printf("generatePDF 开始，路径列表: %v", relPaths)
	if len(relPaths) == 0 {
		return "", fmt.Errorf("no images provided")
	}

	var absPaths []string
	for _, rel := range relPaths {
		abs := filepath.Join(a.uploadDir, rel)
		if _, err := os.Stat(abs); err == nil {
			absPaths = append(absPaths, abs)
		} else {
			log.Printf("图片不存在: %s", abs)
		}
	}
	if len(absPaths) == 0 {
		return "", fmt.Errorf("no valid images found")
	}

	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: 595.28, H: 841.89}}) // A4

	for i, absPath := range absPaths {
		pdf.AddPage()
		// 获取图片尺寸
		file, err := os.Open(absPath)
		if err != nil {
			log.Printf("打开图片失败: %v", err)
			continue
		}
		config, _, err := image.DecodeConfig(file)
		file.Close()
		if err != nil {
			log.Printf("解码图片尺寸失败: %v", err)
			continue
		}
		w := float64(config.Width)
		h := float64(config.Height)
		if w <= 0 || h <= 0 {
			log.Printf("无效图片尺寸: w=%.2f, h=%.2f", w, h)
			continue
		}
		// 缩放至页面宽度（左右边距各50，可用宽度495）
		targetWidth := 495.0
		scale := targetWidth / w
		targetHeight := h * scale
		// 如果高度超过页面高度（留上下边距各50，可用高度约742），则继续缩放
		maxHeight := 742.0
		if targetHeight > maxHeight {
			scale = maxHeight / h
			targetWidth = w * scale
			targetHeight = maxHeight
		}
		// 居中对齐
		x := (595.28 - targetWidth) / 2
		y := (841.89 - targetHeight) / 2
		rect := gopdf.Rect{W: targetWidth, H: targetHeight}
		log.Printf("插入图片 [%d/%d]: %s, 尺寸: %.2fx%.2f, 位置: %.2f, %.2f", i+1, len(absPaths), absPath, targetWidth, targetHeight, x, y)
		if err := pdf.Image(absPath, x, y, &rect); err != nil {
			log.Printf("插入图片失败: %v", err)
			continue
		}
	}

	// 确保PDF目录存在
	if err := os.MkdirAll(a.pdfDir, 0755); err != nil {
		return "", fmt.Errorf("创建PDF目录失败: %v", err)
	}
	timestamp := time.Now().Format("20060102-150405")
	fileName := fmt.Sprintf("拍瞬传-%s.pdf", timestamp)
	outputPath := filepath.Join(a.pdfDir, fileName)
	if err := pdf.WritePdf(outputPath); err != nil {
		return "", fmt.Errorf("write PDF failed: %v", err)
	}
	return outputPath, nil
}

func getLocalIP() string {
	// 获取所有网络接口
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	// 首选：非回环、非 APIPA、有默认网关的 IPv4 地址
	var candidates []string

	for _, iface := range interfaces {
		// 跳过未运行或环回接口
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}
			ipv4 := ipnet.IP.To4()
			if ipv4 == nil {
				continue
			}
			// 排除 APIPA 地址 (169.254.0.0/16)
			if ipv4[0] == 169 && ipv4[1] == 254 {
				continue
			}
			candidates = append(candidates, ipv4.String())
		}
	}

	if len(candidates) == 0 {
		return ""
	}

	// 进一步筛选：优先选择能与默认网关通信的 IP（通过检查路由表）
	// 简单起见，返回第一个非 APIPA 地址（通常这就是正确的局域网 IP）
	// 如果需要更精确，可以尝试 ping 网关，但会增加延迟，不推荐。
	// 这里直接返回第一个候选地址
	return candidates[0]
}
