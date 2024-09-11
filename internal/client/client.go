package client

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/atopos31/go-veilink/internal/config"
	"github.com/atopos31/go-veilink/pkg"
	"github.com/sirupsen/logrus"
	"github.com/xtaci/smux"
)

type Client struct {
	serverAddr string
	clientID   string
}

func NewClient(conf config.ClientConfig) *Client {
	return &Client{
		serverAddr: fmt.Sprintf("%s:%d", conf.ServerIp, conf.ServerPort),
		clientID:   conf.ClientID,
	}
}

func (c *Client) Run() {
	for {
		err := c.run()
		if err != nil && err != io.EOF {
			logrus.Error(fmt.Sprintf("run error: %v", err))
		}
		logrus.Warn(fmt.Sprintf("Reconnecting..."))
		time.Sleep(time.Second * 2)
	}
}

func (c *Client) run() error {
	conn, err := net.Dial("tcp", c.serverAddr)
	if err != nil {
		return err
	}
	defer conn.Close()
	handshakeReq := pkg.HandshakeReq{ClientID: c.clientID}
	buf, err := handshakeReq.Encode()
	if err != nil {
		return err
	}
	// 发送 handshake 请求
	conn.SetWriteDeadline(time.Now().Add(time.Second * 3))
	_, err = conn.Write(buf)
	conn.SetWriteDeadline(time.Time{})
	if err != nil {
		return err
	}

	// 创建 smux
	smuxconfig := smux.DefaultConfig()
	smuxconfig.KeepAliveInterval = 1 * time.Second
	smuxconfig.KeepAliveTimeout = 2 * time.Second
	mux, err := smux.Client(conn, smuxconfig)
	if err != nil {
		return err
	}
	logrus.Debug("handshake success")
	defer mux.Close()
	for {
		stream, err := mux.AcceptStream()
		if err != nil {
			return err
		}
		go c.handleStream(stream)
	}
}

func (c *Client) handleStream(stream net.Conn) {
	defer stream.Close()
	vp := &pkg.VeilinkProtocol{}
	if err := vp.Decode(stream); err != nil {
		logrus.Error(fmt.Sprintf("Decode error: %v", err))
		return
	}

	var localConn net.Conn
	var err error
	switch vp.PublicProtocol {
	case "tcp":
		localConn, err = net.Dial("tcp", fmt.Sprintf("%s:%d", vp.InternalIP, vp.InternalPort))
		if err != nil {
			logrus.Error(fmt.Sprintf("Dial error: %v", err))
			return
		}
		in, out := pkg.Join(localConn, stream)
		logrus.Infof("in: %d bytes, out: %d bytes", in, out)
	case "udp":
		localConn, err = net.Dial("udp", fmt.Sprintf("%s:%d", vp.InternalIP, vp.InternalPort))
		if err != nil {
			logrus.Error(fmt.Sprintf("Dial error: %v", err))
			return
		}
		go func() {
			defer localConn.Close()
			defer stream.Close()
			buf := make([]byte, 1024*64)
			for {
				nr,err := localConn.Read(buf)
				if err != nil {
					logrus.Error(fmt.Sprintf("Decode error: %v", err))
					break
				}
				p := pkg.UDPpacket(buf[:nr])
				body , err := p.Encode()
				if err != nil {
					logrus.Error(fmt.Sprintf("Encode error: %v", err))
					break
				}

				_,err  = stream.Write(body)
				if err != nil {
					logrus.Error(fmt.Sprintf("Write error: %v", err))
					break
				}
			}
		}()

		p := pkg.UDPpacket{}
		for {
			err := p.Decode(stream)
			if err != nil {
				logrus.Error(fmt.Sprintf("Decode error: %v", err))
				break
			}
			_,err  = localConn.Write(p)
			if err != nil {
				logrus.Error(fmt.Sprintf("Write error: %v", err))
				break
			}
		}
	default:
		logrus.Warn(fmt.Sprintf("Unsupported protocol: %s", vp.PublicProtocol))
		return
	}

}
