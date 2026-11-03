package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

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
	flag.Parse()

	store := session.NewStore(*dir)
	srv, err := server.New(store)
	if err != nil {
		log.Fatal(err)
	}
	go store.WarmSearch()
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("wakeview: http://%s", addr)
	log.Fatal(http.ListenAndServe(addr, srv))
}
