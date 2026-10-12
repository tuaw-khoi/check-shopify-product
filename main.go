package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

func main() {
	showVersion := flag.Bool("version", false, "in phiên bản")
	noBrowser := flag.Bool("no-browser", false, "không tự mở trình duyệt")
	flag.Parse()
	if *showVersion {
		fmt.Println("Shopify Price Checker", appVersion)
		return
	}

	app, err := NewAppServer(URLGuard{})
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal("Không mở được local server: ", err)
	}
	address := "http://" + listener.Addr().String()
	httpServer := &http.Server{
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	fmt.Println("Shopify Price Checker đang chạy tại:", address)
	fmt.Println("Giữ cửa sổ này mở. Nhấn Ctrl+C để dừng.")
	if !*noBrowser {
		go func() {
			time.Sleep(350 * time.Millisecond)
			if err := openBrowser(address); err != nil {
				log.Println("Không tự mở được trình duyệt, hãy mở URL ở trên:", err)
			}
		}()
	}
	go func() {
		if serveErr := httpServer.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			log.Fatal(serveErr)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpServer.Shutdown(shutdownCtx)
}

func openBrowser(address string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		command = exec.Command("open", address)
	default:
		command = exec.Command("xdg-open", address)
	}
	return command.Start()
}
