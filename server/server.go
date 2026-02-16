package main

import (
	"crypto/md5"
	"file-transfer/messages"
	"file-transfer/util"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func handleStorage(msgHandler *messages.MessageHandler, request *messages.StorageRequest) {
	// Extract only the base filename (no directories)
	fileName := filepath.Base(request.FileName)
	log.Println("Attempting to store", fileName)

	// Check if file already exists
	if _, err := os.Stat(fileName); err == nil {
		msgHandler.SendResponse(false, "File already exists")
		msgHandler.Close()
		return
	}

	// Check available disk space
	var stat syscall.Statfs_t
	if err := syscall.Statfs(".", &stat); err != nil {
		msgHandler.SendResponse(false, "Cannot check disk space")
		msgHandler.Close()
		return
	}
	availableSpace := stat.Bavail * uint64(stat.Bsize)
	if availableSpace < request.Size {
		msgHandler.SendResponse(false, "Insufficient disk space")
		msgHandler.Close()
		return
	}

	// Create the file
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
	if err != nil {
		msgHandler.SendResponse(false, err.Error())
		msgHandler.Close()
		return
	}

	// Send OK response to client
	msgHandler.SendResponse(true, "Ready for data")
	md5 := md5.New()
	w := io.MultiWriter(file, md5)
	io.CopyN(w, msgHandler, int64(request.Size)) /* Write and checksum as we go */
	file.Close()

	serverCheck := md5.Sum(nil)

	// Receive client's checksum
	clientCheckMsg, err := msgHandler.Receive()
	if err != nil {
		log.Println("Error receiving checksum:", err)
		os.Remove(fileName) // Clean up the file
		return
	}
	clientCheck := clientCheckMsg.GetChecksum().Checksum

	// Verify checksums and send final response
	if util.VerifyChecksum(serverCheck, clientCheck) {
		log.Println("Successfully stored file.")
		msgHandler.SendResponse(true, "File stored successfully")
	} else {
		log.Println("FAILED to store file. Invalid checksum.")
		os.Remove(fileName) // Delete the corrupted file
		msgHandler.SendResponse(false, "Checksum verification failed")
	}
}

func handleRetrieval(msgHandler *messages.MessageHandler, request *messages.RetrievalRequest) {
	// Extract only the base filename (no directories)
	fileName := filepath.Base(request.FileName)
	log.Println("Attempting to retrieve", fileName)

	// Get file size and make sure it exists
	info, err := os.Stat(fileName)
	if err != nil {
		log.Println("File not found:", err)
		msgHandler.SendRetrievalResponse(false, "File not found", 0)
		msgHandler.Close()
		return
	}

	msgHandler.SendRetrievalResponse(true, "Ready to send", uint64(info.Size()))

	file, err := os.Open(fileName)
	if err != nil {
		log.Println("Error opening file:", err)
		return
	}
	defer file.Close()

	md5 := md5.New()
	w := io.MultiWriter(msgHandler, md5)
	io.CopyN(w, file, info.Size()) // Checksum and transfer file at same time

	checksum := md5.Sum(nil)
	msgHandler.SendChecksumVerification(checksum)
}

func handleClient(msgHandler *messages.MessageHandler) {
	defer msgHandler.Close()

	for {
		wrapper, err := msgHandler.Receive()
		if err != nil {
			log.Println(err)
		}

		switch msg := wrapper.Msg.(type) {
		case *messages.Wrapper_StorageReq:
			handleStorage(msgHandler, msg.StorageReq)
			continue
		case *messages.Wrapper_RetrievalReq:
			handleRetrieval(msgHandler, msg.RetrievalReq)
			continue
		case nil:
			log.Println("Received an empty message, terminating client")
			return
		default:
			log.Printf("Unexpected message type: %T", msg)
		}
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf("Not enough arguments. Usage: %s port [download-dir]\n", os.Args[0])
		os.Exit(1)
	}

	port := os.Args[1]
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalln(err.Error())
		os.Exit(1)
	}
	defer listener.Close()

	dir := "."
	if len(os.Args) >= 3 {
		dir = os.Args[2]
	}
	if err := os.Chdir(dir); err != nil {
		log.Fatalln(err)
	}

	// Verify storage directory exists and is writable
	info, err := os.Stat(".")
	if err != nil {
		log.Fatalln("Storage directory does not exist:", err)
	}
	if !info.IsDir() {
		log.Fatalln("Storage path is not a directory")
	}

	fmt.Println("Listening on port:", port)
	fmt.Println("Download directory:", dir)
	for {
		if conn, err := listener.Accept(); err == nil {
			log.Println("Accepted connection", conn.RemoteAddr())
			handler := messages.NewMessageHandler(conn)
			go handleClient(handler)
		}
	}
}
