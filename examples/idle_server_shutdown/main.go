package main

import (
	"context"
	"net/http"
	"time"

	"go.balki.me/anyhttp/idle"
)

func main() {
	idler := idle.CreateIdler(10 * time.Second)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		idler.Tick()
		w.Write([]byte("Hey there!\n"))
	})

	http.HandleFunc("/job", func(w http.ResponseWriter, r *http.Request) {
		go func() {
			idler.Enter()
			defer idler.Exit()

			time.Sleep(15 * time.Second)
		}()
		w.Write([]byte("Job scheduled\n"))
	})

	server := http.Server{
		Addr: ":8888",
	}
	go server.ListenAndServe()
	idler.Wait()
	server.Shutdown(context.TODO())
}
