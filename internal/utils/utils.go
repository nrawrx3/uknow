package utils

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type TCPAddress struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func trimProtocolPrefix(addr string) string {
	addr = strings.TrimPrefix(addr, "udp://")
	addr = strings.TrimPrefix(addr, "tcp://")
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimPrefix(addr, "http://")
	return addr
}

func (t *TCPAddress) SetHostPort(host string, port int) {
	host = trimProtocolPrefix(host)
	t.Host = host
	t.Port = port
}

func (t *TCPAddress) HTTPAddress() string {
	return fmt.Sprintf("http://%s:%d", t.Host, t.Port)
}

func (t *TCPAddress) String() string {
	return fmt.Sprintf("%s:%d", t.Host, t.Port)
}

func CreateFileLogger(setAsDefault bool, playerName string) *log.Logger {
	fileName := fmt.Sprintf("/tmp/%s_log.txt", playerName)
	f, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		log.Fatalf("Failed to open/create log file: %s", fileName)
	}

	if setAsDefault {
		log.SetOutput(f)
		return log.Default()
	} else {
		return log.New(f, playerName, log.Ltime|log.Lshortfile)
	}
}

func ConcatHostPort(protocol string, host string, port int) string {
	if protocol == "" {
		return fmt.Sprintf("%s:%d", host, port)
	}
	return fmt.Sprintf("%s://%s:%d", protocol, host, port)
}

func ResolveTCPAddress(addr string) (TCPAddress, error) {
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return TCPAddress{}, err
	}
	return TCPAddress{
		Host: tcpAddr.IP.String(),
		Port: tcpAddr.Port,
	}, nil
}

func CreateHTTPClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 5,                // We rarely, if at all, make many parallel requests to any host, so 5 is a decent pool size.
		IdleConnTimeout:     10 * time.Minute, // We are working with long connection duration in our gamey use case.
	}

	return &http.Client{
		Timeout:   2 * time.Second,
		Transport: transport,
	}
}

func MakeHTTPRequestWithTimeout(
	parentContext context.Context,
	client *http.Client,
	timeout time.Duration,
	method, url string,
	bodyReader io.Reader) (*http.Response, error) {

	ctx, cancel := context.WithTimeout(parentContext, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bodyReader)
	resp, err := client.Do(req)
	return resp, err
}
