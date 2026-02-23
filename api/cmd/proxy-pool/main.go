package main

import (
	"log"

	"proxy-pool/api/src/server"
)

func main() {
	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
