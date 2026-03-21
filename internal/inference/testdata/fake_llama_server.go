//go:build ignore

// fake_llama_server is a minimal HTTP server that speaks the llama-server
// health API. It is used in tests to exercise the startServer success path
// in LocalBackend without requiring a real llama.cpp binary.
//
// Build with: go build -o fake_llama_server fake_llama_server.go
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
)

func main() {
	// Accept the same flags that llama-server does (we ignore them).
	flag.String("model", "", "model path")
	flag.String("port", "8081", "port")
	flag.String("ctx-size", "4096", "context size")
	flag.Int("n-gpu-layers", 0, "gpu layers")
	flag.Parse()

	port := "8081"
	for i, arg := range os.Args[1:] {
		if arg == "--port" && i+1 < len(os.Args[1:]) {
			port = os.Args[i+2]
			break
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	})

	addr := "127.0.0.1:" + port
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
