package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type HostPortProtocol struct {
	IP       string `json:"ip"`
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

func (t *HostPortProtocol) SetHostPort(ip string, port int) {
	ip = trimProtocolPrefix(ip)
	t.IP = ip
	t.Port = port
}

// Ignore the Protocol, return the http address. If port is 0, doesn't prepend it.
func (t *HostPortProtocol) HTTPAddressString() string {
	if t.Port != 0 {
		return fmt.Sprintf("http://%s:%d", t.IP, t.Port)
	} else {
		return fmt.Sprintf("http://%s", t.IP)
	}
}

// This is the address string to use as arguments to net.Dial or net.Listen
// functions.
func (t *HostPortProtocol) BindString() string {
	if t.Port != 0 {
		return fmt.Sprintf("%s:%d", t.IP, t.Port)
	}
	return t.IP
}

// func ConcatHostPort(protocol string, host string, port int) string {
// 	if protocol == "" {
// 		return fmt.Sprintf("%s:%d", host, port)
// 	}
// 	return fmt.Sprintf("%s://%s:%d", protocol, host, port)
// }

func ResolveTCPAddress(addr string) (HostPortProtocol, error) {
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
		return HostPortProtocol{}, err
	}
	return HostPortProtocol{
		IP:       tcpAddr.IP.String(),
		Port:     tcpAddr.Port,
		Protocol: protocol,
	}, nil
}

func CreateHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		MaxIdleConns: 20,

		// We rarely, if at all, make many parallel requests to any
		// host, so 5 is a decent pool size.
		MaxIdleConnsPerHost: 5,

		// We are working with long connection duration in our gamey use
		// case.
		IdleConnTimeout: 5 * time.Minute,
	}

	return &http.Client{
		Timeout:   timeout,
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

func SetSSEResponseHeaders(responseWriter http.ResponseWriter) {
	responseWriter.Header().Set("Content-Type", "text/event-stream")
	responseWriter.Header().Set("Cache-Control", "no-cache")
	responseWriter.Header().Set("Connection", "keep-alive")
}

func WriteJsonWithNewline(w io.Writer, data interface{}) error {
	encoder := json.NewEncoder(w)
	err := encoder.Encode(data)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "\n")
	if err != nil {
		return err
	}
	return nil
}
