// Command fakes3 runs an in-memory S3 server so media uploads work offline.
// Local development only: no auth, no TLS, no persistence.
package main

import (
	"cmp"
	"log"
	"net/http"
	"os"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func main() {
	addr := cmp.Or(os.Getenv("FAKES3_ADDR"), ":9000")
	bucket := cmp.Or(os.Getenv("FAKES3_BUCKET"), "kaku")

	backend := s3mem.New()
	if err := backend.CreateBucket(bucket); err != nil {
		log.Fatalf("fakes3: create bucket %s: %v", bucket, err)
	}
	log.Printf("fakes3: listening on %s, bucket %q", addr, bucket)
	log.Fatal(http.ListenAndServe(addr, gofakes3.New(backend).Server()))
}
