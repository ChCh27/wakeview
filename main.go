package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/ChCh27/wakeview/internal/server"
	"github.com/ChCh27/wakeview/internal/session"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	dir := flag.String("dir", filepath.Join(home, ".claude", "projects"), "Claude Code projects directory")
	port := flag.Int("port", 7777, "port to listen on (localhost only)")
	open := flag.Bool("open", false, "open wakeview in the default browser")
	flag.Parse()

	store := session.NewStore(*dir)
	srv, err := server.New(store)
	if err != nil {
		log.Fatal(err)
	}
	go store.WarmSearch()

	// Listen before opening the browser, so its first request finds the server up.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	log.Printf("wakeview: %s", url)
	if *open {
		if err := openBrowser(url); err != nil {
			log.Printf("open browser: %v", err)
		}
	}
	log.Fatal(http.Serve(ln, srv))
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
