package dendrite

import (
	"bufio"
	"bytes"
	"errors"
	"github.com/fizx/logs"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var tr = &http.Transport{
	TLSClientConfig:    nil,
	DisableCompression: true,
}

type noOpReader struct{}
type rwStruct struct {
	io.Reader
	io.Writer
}

type libratoStruct struct {
	url       *url.URL
	responses chan string
	metrics   chan []byte
}

var EmptyReader = new(noOpReader)

func (er *noOpReader) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

func NewReadWriter(u *url.URL) (io.ReadWriter, error) {
	protocol := strings.Split(u.Scheme, "+")[0]
	switch protocol {
	case "file":
		return NewFileReadWriter(u.Host + "/" + u.Path)
	case "udp":
		return NewUDPReadWriter(u)
	case "tcp":
		return NewTCPReadWriter(u)
	case "librato":
		return NewLibratoReadWriter(u)
	case "tcps", "tcp+tls":
		panic("not implemented")
	case "http", "https":
		panic("not implemented")
	default:
		panic("unknown protocol")
	}
	return nil, nil //unreached
}

func NewFileReadWriter(path string) (io.ReadWriter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0777)
	if err != nil {
		return nil, err
	}
	return &rwStruct{EmptyReader, file}, nil
}

func NewUDPReadWriter(u *url.URL) (io.ReadWriter, error) {
	conn, err := net.Dial(u.Scheme, u.Host)
	if err != nil {
		return nil, err
	}
	return &rwStruct{EmptyReader, conn}, nil
}

func NewTCPReadWriter(u *url.URL) (io.ReadWriter, error) {
	conn, err := net.Dial(u.Scheme, u.Host)
	if err != nil {
		return nil, err
	}
	return &rwStruct{bufio.NewReader(conn), bufio.NewWriter(conn)}, nil
}

func NewLibratoReadWriter(u *url.URL) (io.ReadWriter, error) {
	rw := new(libratoStruct)
	rw.url = u
	rw.url.Scheme = "https"
	rw.metrics = make(chan []byte, 1000)
	rw.responses = make(chan string, 1000)
	go rw.Loop()
	return rw, nil
}

func (rw *libratoStruct) Loop() {
	var msg []byte
	limit := 300
	msgs := make([][]byte, 0, limit)
	for {
		select {
		case msg = <-rw.metrics:
			msgs = append(msgs, msg)
			continue
		default:
			if len(msgs) > 0 {
				rw.Send(msgs)
				msgs = msgs[0:0]
			}
		}
		time.Sleep(time.Second / 10)
	}
}

func (rw *libratoStruct) Send(msgs [][]byte) {
	body := "{\"gauges\": [" + string(bytes.Join(msgs, []byte(","))) + "]}"
	resp, err := http.Post(rw.url.String(), "application/json", bytes.NewBufferString(body))
	if err != nil {
		logs.Error("error on http request: %s", err)
	} else {
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			logs.Error("error reading http response: %s", err)
		} else {
			rw.responses <- resp.Status + "\n" + string(body)
		}
	}
}

func (rw *libratoStruct) Read(buf []byte) (int, error) {
	rsp := <-rw.responses
	n := copy(buf, rsp)
	if n < len(rsp) {
		return n, errors.New("response truncated")
	} else {
		return n, nil
	}
}

func (rw *libratoStruct) Write(msg []byte) (int, error) {
	rw.metrics <- msg
	return len(msg), nil
}
