package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"go.balki.me/anyhttp"
)

func main() {

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello\n"))
	})
	//log.Println("Got error: ", anyhttp.ListenAndServe(os.Args[1], nil))
	ctx, err := anyhttp.Serve(os.Args[1], nil)
	log.Printf("Got ctx: %v\n,  err: %v", ctx, err)
	log.Println(ctx.Addr())
	if err != nil {
		return
	}
	select {
	case doneErr := <-ctx.Done:
		log.Println(doneErr)
	case <-time.After(1 * time.Minute):
		log.Println("Awake")
		ctx.Shutdown(context.TODO())
	}
}
