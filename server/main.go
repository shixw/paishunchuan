package main

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type ImageRecord struct {
	Filename     string
	OriginalName string
	Size         int64
	UploadTime   time.Time
	URL          string
	Tag          string
}

type ClientInfo struct {
	IP         string
	LastActive time.Time
}

type TagGroup struct {
	Tag    string
	Images []ImageRecord
}

var (
	images      []ImageRecord
	imagesMutex sync.RWMutex
	uploadDir   = "received_images"
	serverPort  = "5000"

	clients      = make(map[string]ClientInfo)
	clientsMutex sync.RWMutex
)

func main() {
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Fatal("创建目录失败:", err)
	}

	tmpl := parseTemplate()

	http.Handle("/static/", http.FileServer(http.FS(staticFS)))

	http.HandleFunc("/delete-image", handleDeleteImage)

	http.HandleFunc("/open-dir", handleOpenDir)
	http.HandleFunc("/upload", handleUpload)
	http.HandleFunc("/health", handleHealth)
	http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		showStatus(w, r, tmpl)
	})
	http.Handle("/received_images/", http.StripPrefix("/received_images/", http.FileServer(http.Dir(uploadDir))))

	go cleanOldClients()

	addr := ":" + serverPort
	localIP := getLocalIP()
	log.Printf("✅ 服务启动，状态页: http://%s%s/status", localIP, addr)
	if localIP != "" {
		log.Printf("📍 本机局域网 IP: %s", localIP)
	}
	go openBrowser(fmt.Sprintf("http://%s:%s/status", localIP, serverPort))

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal("启动失败:", err)
	}
}

func handleDeleteImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	imagePath := r.FormValue("path")
	if imagePath == "" {
		http.Error(w, "缺少 path 参数", http.StatusBadRequest)
		return
	}

	// 1. URL 解码（处理中文字符）
	decodedPath, err := url.PathUnescape(imagePath)
	if err != nil {
		log.Printf("URL解码失败: %v", err)
		http.Error(w, "无效的路径编码", http.StatusBadRequest)
		return
	}

	// 2. 安全检查：确保路径以 /received_images/ 开头
	cleanPath := filepath.Clean(decodedPath)
	if !strings.HasPrefix(cleanPath, "/received_images/") {
		http.Error(w, "无效的路径", http.StatusBadRequest)
		return
	}

	// 3. 构建绝对路径，防止目录遍历
	absPath := filepath.Join(".", cleanPath)
	uploadAbs, _ := filepath.Abs(uploadDir)
	targetAbs, _ := filepath.Abs(absPath)
	if !strings.HasPrefix(targetAbs, uploadAbs+string(os.PathSeparator)) {
		http.Error(w, "路径越界", http.StatusForbidden)
		return
	}

	// 4. 删除磁盘文件
	if err := os.Remove(absPath); err != nil {
		log.Printf("删除文件失败: %v", err)
		http.Error(w, "删除文件失败", http.StatusInternalServerError)
		return
	}

	// 5. 从内存切片中删除记录
	imagesMutex.Lock()
	defer imagesMutex.Unlock()
	for i, img := range images {
		if img.URL == imagePath || img.URL == decodedPath { // 兼容两种
			images = append(images[:i], images[i+1:]...)
			break
		}
	}

	log.Printf("已删除图片: %s", decodedPath)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"success": true}`)
}

// 处理打开目录的请求
func handleOpenDir(w http.ResponseWriter, r *http.Request) {
	// 仅允许 POST 或 GET 简单请求
	absPath, err := filepath.Abs(uploadDir)
	if err != nil {
		http.Error(w, "无法获取目录路径", http.StatusInternalServerError)
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", absPath)
	case "darwin":
		cmd = exec.Command("open", absPath)
	case "linux":
		cmd = exec.Command("xdg-open", absPath)
	default:
		http.Error(w, "不支持的操作系统", http.StatusNotImplemented)
		return
	}

	err = cmd.Start()
	if err != nil {
		log.Printf("打开目录失败: %v", err)
		http.Error(w, "打开目录失败", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"success": true, "path": "%s"}`, absPath)
}

func getClientIP(r *http.Request) string {
	ip := r.RemoteAddr
	host, _, err := net.SplitHostPort(ip)
	if err != nil {
		return ip
	}
	return host
}

func recordClientActivity(r *http.Request) {
	ip := getClientIP(r)
	if ip == "" {
		return
	}
	clientsMutex.Lock()
	defer clientsMutex.Unlock()
	clients[ip] = ClientInfo{IP: ip, LastActive: time.Now()}
}

func cleanOldClients() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		clientsMutex.Lock()
		now := time.Now()
		for ip, info := range clients {
			if now.Sub(info.LastActive) > 30*time.Second {
				delete(clients, ip)
			}
		}
		clientsMutex.Unlock()
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	recordClientActivity(r)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	recordClientActivity(r)

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

	tagDir := filepath.Join(uploadDir, tag)
	if err := os.MkdirAll(tagDir, 0755); err != nil {
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
		http.Error(w, "保存失败", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	size, err := io.Copy(out, file)
	if err != nil {
		http.Error(w, "写入失败", http.StatusInternalServerError)
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

	imagesMutex.Lock()
	// 插入到切片头部（后上传的放到前面）
	images = append([]ImageRecord{record}, images...)
	// 只保留最新的50张（即前50个元素）
	if len(images) > 50 {
		images = images[:50]
	}
	imagesMutex.Unlock()

	log.Printf("📸 收到图片: tag=%s, %s (原: %s, 大小: %d 字节)", tag, newName, header.Filename, size)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"success": true, "filename": "%s", "tag": "%s"}`, newName, tag)
}

func showStatus(w http.ResponseWriter, r *http.Request, tmpl *template.Template) {
	imagesMutex.RLock()
	// 按标签分组，每组内图片顺序保持全局切片顺序（已是后上传的在前）
	groupMap := make(map[string][]ImageRecord)
	for _, img := range images {
		groupMap[img.Tag] = append(groupMap[img.Tag], img)
	}
	groups := make([]TagGroup, 0, len(groupMap))
	for tag, imgs := range groupMap {
		groups = append(groups, TagGroup{Tag: tag, Images: imgs})
	}
	// 按标签名排序分组顺序
	sort.Slice(groups, func(i, j int) bool {
		return groups[i].Tag < groups[j].Tag
	})
	imagesMutex.RUnlock()

	clientsMutex.RLock()
	clientList := make([]ClientInfo, 0, len(clients))
	for _, c := range clients {
		clientList = append(clientList, c)
	}
	clientsMutex.RUnlock()
	sort.Slice(clientList, func(i, j int) bool {
		return clientList[i].LastActive.After(clientList[j].LastActive)
	})

	localIP := getLocalIP()
	if localIP == "" {
		localIP = "127.0.0.1"
	}
	serverURL := fmt.Sprintf("http://%s:%s", localIP, serverPort)

	data := struct {
		ServerURL string
		Groups    []TagGroup
		Clients   []ClientInfo
		Refresh   int
	}{
		ServerURL: serverURL,
		Groups:    groups,
		Clients:   clientList,
		Refresh:   5,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("模板渲染失败: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func parseTemplate() *template.Template {
	content, err := templateFS.ReadFile("templates/status.html")
	if err != nil {
		log.Fatal("读取模板失败:", err)
	}
	tmpl := template.New("status").Funcs(template.FuncMap{
		"now": func() string { return time.Now().Format("2006-01-02 15:04:05") },
	})
	_, err = tmpl.Parse(string(content))
	if err != nil {
		log.Fatal("解析模板失败:", err)
	}
	return tmpl
}

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

func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		log.Printf("自动打开浏览器失败: %v, 请手动访问 %s", err, url)
	}
}
