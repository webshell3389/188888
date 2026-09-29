// Command mockc2 启动入口：CLI -> 自签证书 -> HTTP + HTTPS 双栈；支持多实例/多 worker 横向扩展。
//
// 单实例：
//
//	mockc2                          # HTTP :9100 + HTTPS :9443（证书自动生成）
//	mockc2 --no-https               # 仅 HTTP :9100（联调最快）
//	mockc2 --cert cert.pem --key key.pem --http-port 8888
//
// 多 worker（父进程终结 TLS + 内置轮询 LB，子进程端口 = HTTP端口+1..+N）：
//
//	mockc2 --workers 4              # LB :9100/:9443 -> workers :9101..:9104
//	mockc2 --workers 4 --no-https
//
// 外部/跨机后端（父进程仅做 LB，状态经共享 SQLite/共享盘一致）：
//
//	mockc2 --backends http://10.0.0.11:9100,http://10.0.0.12:9100
//
// 说明：Go 单进程本身已用满多核（goroutine 调度到全部 P），--workers 用于
// 进程级隔离/独立重启/跨机横向扩展；多实例共享状态已外置到 SQLite(command_queue/kv)
// 与共享盘(capture/.parts)，会话为无状态签名 token，任意节点可校验。
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	httppprof "net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mockc2/internal/config"
	"mockc2/internal/server"
	"mockc2/internal/store"
)

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

var ipRe = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}$`)

func looksIP(s string) bool { return ipRe.MatchString(s) }

// ── 自签证书（CA + 服务器证书）──
func ensureCerts(certPath, keyPath, caPath string, domains []string) error {
	if fileExists(certPath) && fileExists(keyPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		return err
	}
	caKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}
	caTpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Mock C2 Root CA", Organization: []string{"MockLab"}, Country: []string{"CN"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	if err := writePEM(caPath, "CERTIFICATE", caDER); err != nil {
		return err
	}
	if err := writePEM(filepath.Join(filepath.Dir(caPath), "ca.key"), "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caKey)); err != nil {
		return err
	}

	srvKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	cn := domains[0]
	tpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"MockLab"}, Country: []string{"CN"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(2, 3, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, d := range domains {
		if looksIP(d) {
			if ip := net.ParseIP(d); ip != nil {
				tpl.IPAddresses = append(tpl.IPAddresses, ip)
			}
		} else {
			tpl.DNSNames = append(tpl.DNSNames, d)
		}
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, tpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	if err := writePEM(certPath, "CERTIFICATE", srvDER); err != nil {
		return err
	}
	if err := writePEM(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(srvKey)); err != nil {
		return err
	}
	fmt.Printf("[i] 证书已生成: %s, CA 信任: %s, SAN: %s\n",
		filepath.Base(certPath), filepath.Base(caPath), strings.Join(domains, ","))
	return nil
}

func serial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, _ := rand.Int(rand.Reader, limit)
	return n
}

func writePEM(path, blockType string, der []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// newHTTPServer 高并发友好配置：连接超时防 Slowloris，宽松 Keep-Alive。
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

// resolveCertPaths 计算证书路径 + SAN，并生成（若缺失）。
func resolveCertPaths(s *config.Settings, certFlag, keyFlag string, domains []string) (cert, key, ca string, ok bool) {
	if err := os.MkdirAll(s.CertsDir, 0o755); err != nil {
		return "", "", "", false
	}
	cert = certFlag
	if cert == "" {
		cert = filepath.Join(s.CertsDir, "cert.pem")
	}
	key = keyFlag
	if key == "" {
		key = filepath.Join(s.CertsDir, "key.pem")
	}
	ca = filepath.Join(s.CertsDir, "ca.pem")
	all := append([]string{}, s.CertDomains...)
	all = append(all, domains...)
	if len(all) == 0 {
		all = []string{"localhost"}
	}
	if err := ensureCerts(cert, key, ca, all); err != nil {
		fmt.Printf("[!] 证书生成/加载失败（HTTPS 跳过）：%v\n", err)
		return "", "", "", false
	}
	return cert, key, ca, true
}

func main() {
	s := config.Get()

	host := flag.String("host", "", "监听地址")
	httpPort := flag.Int("http-port", 0, "HTTP 端口")
	httpsPort := flag.Int("https-port", 0, "HTTPS 端口")
	noHTTPS := flag.Bool("no-https", false, "仅 HTTP")
	certFlag := flag.String("cert", "", "服务器证书路径")
	keyFlag := flag.String("key", "", "服务器私钥路径")
	workers := flag.Int("workers", 1, "worker 进程数（>1 时父进程内置 LB，子进程端口=HTTP端口+1..+N）")
	backends := flag.String("backends", "", "外部后端列表（逗号分隔 http://host:port）；设置后父进程仅做 LB")
	workerChild := flag.Bool("worker-child", false, "内部：worker 子进程标记")
	pprofAddr := flag.String("pprof-addr", "127.0.0.1:6060", "pprof 诊断监听（仅本机；空字符串=关闭）")
	var domains multiFlag
	flag.Var(&domains, "domain", "证书 SAN 域名/IP（可多次传入；默认 localhost+本机 IP）")
	flag.Parse()

	// pprof 独立监听：本次故障（INC-2026-0929-001）教训 —— 必须能非破坏性抓栈/抓 profile。
	if *pprofAddr != "" {
		go servePprof(*pprofAddr)
	}

	if *workerChild {
		*workers = 1
	}
	if *host != "" {
		s.Host = *host
	}
	if *httpPort != 0 {
		s.HTTPPort = *httpPort
	}
	if *httpsPort != 0 {
		s.HTTPSPort = *httpsPort
	}

	// ── 外部后端：父进程仅做 LB ──
	if *backends != "" {
		targets := splitTrim(*backends)
		fmt.Printf("[*] Mock C2 LB（外部后端）  HTTP %s:%d -> %v\n", s.Host, s.HTTPPort, targets)
		runLB(s, targets, *noHTTPS, *certFlag, *keyFlag, domains)
		return
	}

	// ── 多 worker：父进程一次性迁移 + 拉子进程 + 内置 LB ──
	if *workers > 1 {
		if err := store.MigrateOnce(server.DBConfigFromSettings(s)); err != nil {
			log.Fatalf("[!] 迁移失败: %v", err)
		}
		cmds, targets := spawnWorkers(s, *workers)
		setupChildCleanup(cmds)
		if len(targets) == 0 {
			log.Fatalf("[!] 无可用 worker")
		}
		fmt.Printf("[*] Mock C2 LB 启动  workers=%d  role=delivery+c2\n", len(targets))
		fmt.Printf("    HTTP  http://%s:%d  -> %s\n", s.Host, s.HTTPPort, strings.Join(targets, " "))
		runLB(s, targets, *noHTTPS, *certFlag, *keyFlag, domains)
		return
	}

	// ── 单实例 ──
	if err := server.InitServices(s); err != nil {
		log.Fatalf("[!] 服务初始化失败: %v", err)
	}
	handler := server.NewHandler()

	fmt.Printf("[*] Mock C2 启动  role=delivery+c2  host=%s\n", s.Host)
	fmt.Printf("    DB    mysql %s:%d/%s  (pool=%d idle=%d)\n",
		s.DBHost, s.DBPort, s.DBName, s.DBMaxOpenConns, s.DBMaxIdleConns)
	fmt.Printf("    HTTP  http://%s:%d\n", s.Host, s.HTTPPort)

	if !*noHTTPS {
		if cert, key, ca, ok := resolveCertPaths(s, *certFlag, *keyFlag, domains); ok {
			fmt.Printf("    HTTPS https://%s:%d  (信任 %s)\n", s.Host, s.HTTPSPort, ca)
			httpsSrv := newHTTPServer(net.JoinHostPort(s.Host, strconv.Itoa(s.HTTPSPort)), handler)
			go func() {
				if err := httpsSrv.ListenAndServeTLS(cert, key); err != nil {
					fmt.Printf("[!] HTTPS 服务退出: %v\n", err)
				}
			}()
		}
	}

	srv := newHTTPServer(net.JoinHostPort(s.Host, strconv.Itoa(s.HTTPPort)), handler)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// runLB 以 LB 模式提供服务（父进程终结 TLS，后端为 HTTP）。
func runLB(s *config.Settings, targets []string, noHTTPS bool, certFlag, keyFlag string, domains []string) {
	handler := server.NewRoundRobinLB(targets)
	waitForPorts(targets, 15*time.Second)
	if !noHTTPS {
		if cert, key, ca, ok := resolveCertPaths(s, certFlag, keyFlag, domains); ok {
			fmt.Printf("    HTTPS https://%s:%d  (信任 %s)\n", s.Host, s.HTTPSPort, ca)
			httpsSrv := newHTTPServer(net.JoinHostPort(s.Host, strconv.Itoa(s.HTTPSPort)), handler)
			go func() {
				if err := httpsSrv.ListenAndServeTLS(cert, key); err != nil {
					fmt.Printf("[!] HTTPS 服务退出: %v\n", err)
				}
			}()
		}
	}
	srv := newHTTPServer(net.JoinHostPort(s.Host, strconv.Itoa(s.HTTPPort)), handler)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// spawnWorkers 拉起 N 个 worker 子进程（端口 = HTTP端口+1..+N，仅 HTTP）。
func spawnWorkers(s *config.Settings, n int) ([]*exec.Cmd, []string) {
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("[!] 无法定位可执行文件: %v", err)
	}
	var cmds []*exec.Cmd
	var targets []string
	for i := 0; i < n; i++ {
		port := s.HTTPPort + 1 + i
		c := exec.Command(exe, "--worker-child", "--no-https", "--http-port", strconv.Itoa(port))
		c.Dir = s.RootDir
		c.Env = append(os.Environ(),
			"MOCKC2_ROOT="+s.RootDir,
			"MOCKC2_INSTANCE_ID=w"+strconv.Itoa(i),
		)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Start(); err != nil {
			fmt.Printf("[!] worker %d 启动失败: %v\n", i, err)
			continue
		}
		cmds = append(cmds, c)
		targets = append(targets, "http://127.0.0.1:"+strconv.Itoa(port))
		fmt.Printf("    worker w%d  http://127.0.0.1:%d  (pid=%d)\n", i, port, c.Process.Pid)
	}
	return cmds, targets
}

var childCmds []*exec.Cmd

func setupChildCleanup(cmds []*exec.Cmd) {
	childCmds = cmds
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		killChildren()
		os.Exit(0)
	}()
}

func killChildren() {
	for _, c := range childCmds {
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	}
}

// waitForPorts 等待后端可连接（最长 timeout）。
func waitForPorts(targets []string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for _, t := range targets {
		hostport := strings.TrimPrefix(strings.TrimPrefix(t, "http://"), "https://")
		for time.Now().Before(deadline) {
			conn, err := net.DialTimeout("tcp", hostport, 300*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func splitTrim(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// servePprof pprof 诊断监听（默认仅本机 127.0.0.1:6060）。
// INC-2026-0929-001 §6.7：故障诊断必须免重启抓取 goroutine 栈 / CPU / heap profile。
func servePprof(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", httppprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", httppprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", httppprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", httppprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", httppprof.Trace)
	fmt.Printf("    PPROF http://%s/debug/pprof/\n", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Printf("[!] pprof 监听退出: %v\n", err)
	}
}
