package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Default values
const (
	defaultPort    = 443
	defaultMaxSize = 50 * 1024 * 1024 // 50 MB
)

func main() {
	// Check for subcommands before parsing server flags
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "proxy":
			handleProxySubcommand(os.Args[2:])
			return
		case "worktree":
			handleWorktreeSubcommand(os.Args[2:])
			return
		}
	}

	// --- Flag parsing ---
	port := flag.Int("port", lookupEnvInt("PORT", defaultPort), "HTTPS server port")
	maxSize := flag.Int("max-size", lookupEnvInt("MAX_UPLOAD_SIZE", defaultMaxSize), "Maximum upload file size in bytes")
	certDir := flag.String("cert-dir", lookupEnvStr("CERT_DIR", defaultCertDir()), "Directory for TLS certificates")
	uploadDir := flag.String("upload-dir", lookupEnvStr("UPLOAD_DIR", defaultUploadDir()), "Upload directory")
	installSvc := flag.Bool("install-service", false, "Install systemd service and exit")
	flag.Parse()

	// Handle --install-service flag
	if *installSvc {
		binPath, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get executable path: %v\n", err)
			os.Exit(1)
		}
		binPath, err = filepath.Abs(binPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to resolve absolute path: %v\n", err)
			os.Exit(1)
		}
		workDir, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get working directory: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Installing systemd service...\n")
		fmt.Printf("  Binary: %s\n", binPath)
		fmt.Printf("  Working directory: %s\n", workDir)

		if err := InstallService(binPath, workDir, "/etc/systemd/system"); err != nil {
			fmt.Fprintf(os.Stderr, "failed to install service: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Service file written to /etc/systemd/system/aru.service\n")
		fmt.Printf("Run: systemctl daemon-reload && systemctl enable --now aru\n")
		os.Exit(0)
	}

	// --- Structured logging ---
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	slog.Info("starting aru server",
		"port", *port,
		"max_size", *maxSize,
		"cert_dir", *certDir,
		"upload_dir", *uploadDir,
	)

	// --- Create upload directory and check writability ---
	if err := os.MkdirAll(*uploadDir, 0755); err != nil {
		slog.Error("failed to create upload directory", "dir", *uploadDir, "error", err)
		os.Exit(1)
	}
	if err := checkWritable(*uploadDir); err != nil {
		slog.Error("upload directory not writable", "dir", *uploadDir, "error", err)
		os.Exit(1)
	}

	// --- Ensure certificate ---
	if err := EnsureCert(*certDir); err != nil {
		slog.Error("failed to ensure certificate", "error", err)
		os.Exit(1)
	}

	// Read cert files
	certPEM, err := os.ReadFile(filepath.Join(*certDir, "cert.pem"))
	if err != nil {
		slog.Error("failed to read cert.pem", "error", err)
		os.Exit(1)
	}
	keyPEM, err := os.ReadFile(filepath.Join(*certDir, "key.pem"))
	if err != nil {
		slog.Error("failed to read key.pem", "error", err)
		os.Exit(1)
	}

	// --- Load proxy database ---
	proxyDBPath := filepath.Join(*certDir, "proxies.json")
	proxyDB, err := LoadProxyDB(proxyDBPath)
	if err != nil {
		slog.Error("failed to load proxy database", "path", proxyDBPath, "error", err)
		os.Exit(1)
	}
	slog.Info("loaded proxy database", "path", proxyDBPath, "entries", proxyDB.Len())

	// --- Create context with signal handling ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- Start cleanup goroutine ---
	StartCleanup(ctx, *uploadDir, 1*time.Hour, 5*time.Minute, 5*time.Minute)

	// --- Start DNS server (non-fatal) ---
	StartDNS(ctx)

	// --- Start HTTP redirect server (non-fatal) ---
	StartRedirect(ctx)

	// --- Create HTTPS server ---
	srv := NewServer(int64(*maxSize), *uploadDir, certPEM, keyPEM, proxyDB)
	if srv == nil {
		slog.Error("failed to create server")
		os.Exit(1)
	}

	// --- Listen on HTTPS port ---
	plainListener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		slog.Error("failed to listen", "port", *port, "error", err)
		os.Exit(1)
	}

	// Parse TLS certificate for the listener
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		slog.Error("failed to parse certificate", "error", err)
		os.Exit(1)
	}
	tlsListener := tls.NewListener(plainListener, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})

	// --- Serve HTTPS ---
	slog.Info("HTTPS server listening",
		"addr", tlsListener.Addr().String(),
		"upload_dir", *uploadDir,
	)

	fmt.Printf("Open https://tmp.test in your browser (requires DNS)\n")

	go func() {
		<-ctx.Done()
		slog.Info("shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	if err := srv.httpServer.Serve(tlsListener); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped")
}

// handleProxySubcommand dispatches to proxy management subcommands.
func handleProxySubcommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: aru proxy <command> [options]\n\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  add    Add a proxy entry  (--name=X --port=Y)\n")
		fmt.Fprintf(os.Stderr, "  del    Delete a proxy entry  (--name=X)\n")
		fmt.Fprintf(os.Stderr, "  list   List all proxy entries\n")
		os.Exit(1)
	}

	switch args[0] {
	case "add":
		handleProxyAdd(args[1:])
	case "del":
		handleProxyDel(args[1:])
	case "list":
		handleProxyList()
	default:
		fmt.Fprintf(os.Stderr, "Unknown proxy command: %q\n", args[0])
		fmt.Fprintf(os.Stderr, "Available: add, del, list\n")
		os.Exit(1)
	}
}

func handleProxyAdd(args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	name := fs.String("name", "", "Proxy name (e.g., myapp)")
	port := fs.Int("port", 0, "Target port (e.g., 3000)")
	fs.Parse(args)

	if *name == "" || *port == 0 {
		fmt.Fprintf(os.Stderr, "Usage: aru proxy add --name=X --port=Y\n")
		fs.Usage()
		os.Exit(1)
	}

	dbPath := defaultProxyDBPath()
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading proxy DB: %v\n", err)
		os.Exit(1)
	}

	if err := db.Add(*name, *port); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Proxy '%s' added → localhost:%d\n", *name, *port)
}

func handleProxyDel(args []string) {
	fs := flag.NewFlagSet("del", flag.ExitOnError)
	name := fs.String("name", "", "Proxy name to delete")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintf(os.Stderr, "Usage: aru proxy del --name=X\n")
		fs.Usage()
		os.Exit(1)
	}

	dbPath := defaultProxyDBPath()
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading proxy DB: %v\n", err)
		os.Exit(1)
	}

	if err := db.Delete(*name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Proxy '%s' deleted\n", *name)
}

func handleProxyList() {
	dbPath := defaultProxyDBPath()
	db, err := LoadProxyDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading proxy DB: %v\n", err)
		os.Exit(1)
	}

	entries := db.List()
	if len(entries) == 0 {
		fmt.Println("No proxy entries configured.")
		return
	}

	// Sort by name for consistent output
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Printf("%-30s %s\n", "NAME", "PORT")
	fmt.Println(strings.Repeat("-", 38))
	for _, name := range names {
		fmt.Printf("%-30s %d\n", name, entries[name])
	}
	fmt.Printf("\n%d proxy entr%s\n", len(entries), map[bool]string{true: "y", false: "ies"}[len(entries) == 1])
}

// defaultProxyDBPath returns the default path for the proxy database JSON file.
func defaultProxyDBPath() string {
	return filepath.Join(defaultCertDir(), "proxies.json")
}

// lookupEnvInt returns the integer value of the environment variable, or fallback.
func lookupEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

// lookupEnvStr returns the value of the environment variable, or fallback.
func lookupEnvStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultCertDir returns the default certificate directory (~/.aru/).
func defaultCertDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".aru"
	}
	return filepath.Join(home, ".aru")
}

// defaultUploadDir returns the default upload directory.
func defaultUploadDir() string {
	return "/tmp/u"
}

// checkWritable verifies that a directory is writable by creating and removing a test file.
func checkWritable(dir string) error {
	testFile := filepath.Join(dir, ".writetest")
	if err := os.WriteFile(testFile, []byte("writability-check"), 0644); err != nil {
		return fmt.Errorf("directory not writable: %w", err)
	}
	os.Remove(testFile)
	return nil
}

// GenerateServiceFile returns the content of the systemd service unit file
// with ExecStart and WorkingDirectory set to the given paths.
func GenerateServiceFile(binPath, workDir string) string {
	return fmt.Sprintf(`[Unit]
Description=aru — development utility server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, workDir, binPath)
}

// InstallService writes the systemd service file to targetDir.
// It creates the target directory if needed.
func InstallService(binPath, workDir, targetDir string) error {
	content := GenerateServiceFile(binPath, workDir)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}
	servicePath := filepath.Join(targetDir, "aru.service")
	if err := os.WriteFile(servicePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write service file %s: %w", servicePath, err)
	}
	return nil
}
