package utils

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type TCPAddress struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
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
	if t.Port != 0 {
		return fmt.Sprintf("http://%s:%d", t.Host, t.Port)
	} else {
		return fmt.Sprintf("http://%s", t.Host)
	}
}

func (t *TCPAddress) String() string {
	protocol := "http"
	if t.Protocol != "" {
		protocol = t.Protocol
	}
	return fmt.Sprintf("%s://%s:%d", protocol, t.Host, t.Port)
}

func (t *TCPAddress) BindString() string {
	return fmt.Sprintf("%s:%d", t.Host, t.Port)
}

func ConcatHostPort(protocol string, host string, port int) string {
	if protocol == "" {
		return fmt.Sprintf("%s:%d", host, port)
	}
	return fmt.Sprintf("%s://%s:%d", protocol, host, port)
}

func ResolveTCPAddress(addr string) (TCPAddress, error) {
	var protocol string

	if strings.HasPrefix(addr, "http://") {
		protocol = "http"
	} else if strings.HasPrefix(addr, "https://") {
		protocol = "https"
	} else if strings.HasPrefix(addr, "tcp://") {
		protocol = "tcp"
	} else {
		protocol = ""
	}

	addr = trimProtocolPrefix(addr)

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		return TCPAddress{}, err
	}
	return TCPAddress{
		Host:     tcpAddr.IP.String(),
		Port:     tcpAddr.Port,
		Protocol: protocol,
	}, nil
}

func CreateHTTPClient() *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 5,                // We rarely, if at all, make many parallel requests to any host, so 5 is a decent pool size.
		IdleConnTimeout:     10 * time.Minute, // We are working with long connection duration in our gamey use case.
	}

	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}
}

type RequestSender struct {
	Client     *http.Client
	Method     string
	URL        string
	BodyReader io.Reader
}

func (sender *RequestSender) SendWithTimeout(parentContext context.Context, timeout time.Duration) (*http.Response, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(parentContext, timeout)

	req, err := http.NewRequestWithContext(ctx, sender.Method, sender.URL, sender.BodyReader)
	if err != nil {
		return nil, cancel, err
	}
	resp, err := sender.Client.Do(req)
	if err != nil {
		return nil, cancel, err
	}
	return resp, cancel, nil
}

func (sender *RequestSender) Send(parentContext context.Context) (*http.Response, error) {
	req, err := http.NewRequestWithContext(parentContext, sender.Method, sender.URL, sender.BodyReader)
	if err != nil {
		return nil, err
	}
	resp, err := sender.Client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
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
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	return resp, err
}
