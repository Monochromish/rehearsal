// Throwaway local HTTPS test server for the proxy spike. It never talks to
// the internet.
//
//	GET  /events  five server-sent events, 400 ms apart
//	POST /echo    replies with the number of bytes it received
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"time"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		for i := 1; i <= 5; i++ {
			if _, err := fmt.Fprintf(w, "data: tick %d\n\n", i); err != nil {
				log.Printf("client went away: %v", err)
				return
			}
			flusher.Flush()
			time.Sleep(400 * time.Millisecond)
		}
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if _, err := fmt.Fprintf(w, "received %d bytes\n", n); err != nil {
			log.Printf("client went away: %v", err)
		}
	})
	srv := httptest.NewTLSServer(mux) // self-signed, 127.0.0.1, random port
	defer srv.Close()
	fmt.Println(srv.URL)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
}
