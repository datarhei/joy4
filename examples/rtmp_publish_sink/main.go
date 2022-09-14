package main

import (
	"flag"
	"fmt"
	"io"
	"net"

	"github.com/datarhei/joy4/format"
	"github.com/datarhei/joy4/format/rtmp"
)

func init() {
	format.RegisterAll()
}

type Config struct {
	Addr  string
	App   string
	Token string
}

type Server interface {
	ListenAndServe() error
	ListenAndServeTLS(certFile, keyFile string) error
}

type server struct {
	app   string
	token string

	server *rtmp.Server
}

var _ Server = &server{}

func New(config Config) (Server, error) {
	if len(config.App) == 0 {
		config.App = "/"
	}

	s := &server{
		app:   config.App,
		token: config.Token,
	}

	s.server = &rtmp.Server{
		Addr: config.Addr,
	}

	s.server.HandlePlay = s.handlePlay
	s.server.HandlePublish = s.handlePublish

	rtmp.Debug = false

	return s, nil
}

func (s *server) ListenAndServe() error {
	return s.server.ListenAndServe()
}

func (s *server) ListenAndServeTLS(certFile, keyFile string) error {
	return s.server.ListenAndServeTLS(certFile, keyFile)
}

func (s *server) log(who, action, path, message string, client net.Addr) {
	fmt.Printf("%-7s %10s %s (%s) %s\n", who, action, path, client, message)
}

func (s *server) handlePlay(conn *rtmp.Conn) {
	client := conn.NetConn().RemoteAddr()

	s.log("PLAY", "INVALID", conn.URL.Path, "not supported", client)
	conn.Close()
}

func (s *server) handlePublish(conn *rtmp.Conn) {
	client := conn.NetConn().RemoteAddr()

	streams, _ := conn.Streams()

	if len(streams) == 0 {
		s.log("PUBLISH", "INVALID", conn.URL.Path, "no streams available", client)
		conn.Close()
		return
	}

	s.log("PUBLISH", "START", conn.URL.Path, "", client)

	for _, stream := range streams {
		s.log("PUBLISH", "STREAM", conn.URL.Path, stream.Type().String(), client)
	}

	for {
		if _, err := conn.ReadPacket(); err != nil {
			if err != io.EOF {
				s.log("PUBLISH", "ERROR", conn.URL.Path, err.Error(), client)
			}

			break
		}
	}

	s.log("PUBLISH", "STOP", conn.URL.Path, "", client)

	return
}

func main() {
	var cert string
	var key string
	var help bool
	var addr string
	var app string
	var token string

	flag.StringVar(&cert, "cert", "", "Path to the certifacate file")
	flag.StringVar(&key, "key", "", "Path to the key file")
	flag.StringVar(&addr, "addr", ":1935", "Address to listen on")
	flag.StringVar(&app, "app", "/", "RTMP app, should start with /")
	flag.StringVar(&token, "token", "", "Token query string")
	flag.BoolVar(&help, "h", false, "Show options")

	flag.Parse()

	config := Config{
		Addr:  addr,
		App:   app,
		Token: token,
	}

	server, _ := New(config)

	var err error

	if len(cert) == 0 && len(key) == 0 {
		fmt.Printf("Started RTMP server. Listening on %s\n", config.Addr)
		err = server.ListenAndServe()
	} else {
		fmt.Printf("Started RTMPS server. Listening on %s\n", config.Addr)
		err = server.ListenAndServeTLS(cert, key)
	}

	fmt.Printf("%s\n", err)
}
