package upstream

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func sshKey(t *testing.T) (signer ssh.Signer, privatePEM string) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err = ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	return signer, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func TestSSHDeployWithPinnedHostAndRestrictedFiles(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".tmp", "ssh-test-"+rand.Text()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	host, _ := sshKey(t)
	user, private := sshKey(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	done := make(chan error, 1)
	reloaded := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), user.PublicKey().Marshal()) {
				return nil, errors.New("unexpected client key")
			}
			return &ssh.Permissions{}, nil
		}}
		config.AddHostKey(host)
		connection, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			done <- err
			return
		}
		defer connection.Close()
		go ssh.DiscardRequests(requests)
		for channel := range channels {
			stream, reqs, err := channel.Accept()
			if err != nil {
				done <- err
				return
			}
			go func() {
				defer stream.Close()
				for req := range reqs {
					switch req.Type {
					case "subsystem":
						req.Reply(true, nil)
						server, err := sftp.NewServer(stream)
						if err != nil {
							t.Error(err)
							return
						}
						if err := server.Serve(); err != nil && !errors.Is(err, io.EOF) {
							t.Error(err)
						}
						server.Close()
						return
					case "exec":
						var command struct{ Value string }
						ssh.Unmarshal(req.Payload, &command)
						reloaded <- command.Value
						req.Reply(true, nil)
						stream.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
						return
					default:
						req.Reply(false, nil)
					}
				}
			}()
		}
		done <- nil
	}()
	cfg := certconfig.Upstream{Kind: "ssh", Address: listener.Addr().String(), User: "deploy", PrivateKey: private, HostKey: string(ssh.MarshalAuthorizedKey(host.PublicKey())), CertificatePath: filepath.Join(root, "fullchain.pem"), KeyPath: filepath.Join(root, "privkey.pem"), ReloadCommand: "reload-certificates"}
	receipt, err := (Registry{}).Upload(t.Context(), cfg, Bundle{Fingerprint: "fingerprint", Certificate: []byte("chain"), PrivateKey: []byte("private")})
	if err != nil || receipt != "fingerprint" {
		t.Fatal("SSH deploy", receipt, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if command := <-reloaded; command != "reload-certificates" {
		t.Fatal("reload missing")
	}
	for path, expected := range map[string]string{cfg.CertificatePath: "chain", cfg.KeyPath: "private"} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != expected {
			t.Fatal("wrong deployed file", err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("unsafe file permissions", err)
		}
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 2 {
		t.Fatal("temporary remote files remain", err)
	}
}

func TestSSHRejectsWrongHostKey(t *testing.T) {
	host, _ := sshKey(t)
	wrong, _ := sshKey(t)
	_, private := sshKey(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		config := &ssh.ServerConfig{NoClientAuth: true}
		config.AddHostKey(host)
		connection, _, _, err := ssh.NewServerConn(conn, config)
		if err == nil {
			connection.Close()
		}
	}()
	_, err = (Registry{}).Upload(t.Context(), certconfig.Upstream{Kind: "ssh", Address: listener.Addr().String(), User: "deploy", PrivateKey: private, HostKey: string(ssh.MarshalAuthorizedKey(wrong.PublicKey()))}, Bundle{})
	if err == nil {
		t.Fatal("wrong SSH host key accepted")
	}
	<-done
}
