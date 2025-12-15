package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/dongxuny/agfs-fuse/pkg/fusefs"
	"github.com/dongxuny/agfs-fuse/pkg/version"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	log "github.com/sirupsen/logrus"
)

func main() {
	var (
		serverURL   = flag.String("agfs-server-url", "http://localhost:8080", "AGFS server URL")
		mountpoint  = flag.String("mount", "", "Mount point directory")
		cacheTTL    = flag.Duration("cache-ttl", 5*time.Second, "Cache TTL duration")
		debug       = flag.Bool("debug", false, "Enable debug output")
		logLevel    = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
		allowOther  = flag.Bool("allow-other", false, "Allow other users to access the mount")
		showVersion = flag.Bool("version", false, "Show version information")
	)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Mount AGFS server as a FUSE filesystem.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  %s --agfs-server-url http://localhost:8080 --mount /mnt/agfs\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --agfs-server-url http://localhost:8080 --mount /mnt/agfs --cache-ttl=10s\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s --agfs-server-url http://localhost:8080 --mount /mnt/agfs --debug\n", os.Args[0])
	}

	flag.Parse()

	// Show version
	if *showVersion {
		fmt.Printf("agfs-fuse %s\n", version.GetFullVersion())
		os.Exit(0)
	}

	// Initialize logrus
	level := log.InfoLevel
	if *debug {
		level = log.DebugLevel
	} else if *logLevel != "" {
		if parsedLevel, err := log.ParseLevel(*logLevel); err == nil {
			level = parsedLevel
		}
	}
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			filename := filepath.Base(f.File)
			return "", fmt.Sprintf(" | %s:%d | ", filename, f.Line)
		},
	})
	log.SetReportCaller(true)
	log.SetLevel(level)

	// Check required arguments
	if *mountpoint == "" {
		fmt.Fprintf(os.Stderr, "Error: --mount is required\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// Create filesystem
	root := fusefs.NewAGFSFS(fusefs.Config{
		ServerURL: *serverURL,
		CacheTTL:  *cacheTTL,
		Debug:     *debug,
	})

	// Setup FUSE mount options
	opts := &fs.Options{
		AttrTimeout:  cacheTTL,
		EntryTimeout: cacheTTL,
		MountOptions: fuse.MountOptions{
			Name:          "agfs",
			FsName:        "agfs",
			DisableXAttrs: true,
			Debug:         *debug,
		},
	}

	if *allowOther {
		opts.MountOptions.AllowOther = true
	}

	// Mount the filesystem
	server, err := fs.Mount(*mountpoint, root, opts)
	if err != nil {
		log.Fatalf("Mount failed: %v", err)
	}

	log.Infof("AGFS mounted at %s", *mountpoint)
	log.Infof("Server: %s", *serverURL)
	log.Infof("Cache TTL: %v", *cacheTTL)

	if level > log.DebugLevel {
		log.Info("Press Ctrl+C to unmount")
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Info("Unmounting...")

		// Unmount
		if err := server.Unmount(); err != nil {
			log.Errorf("Unmount failed: %v", err)
		}

		// Close filesystem
		if err := root.Close(); err != nil {
			log.Errorf("Close filesystem failed: %v", err)
		}
	}()

	// Wait for the filesystem to be unmounted
	server.Wait()

	log.Info("AGFS unmounted successfully")
}
