package upstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func uploadSSH(ctx context.Context, cfg certconfig.Upstream, bundle Bundle) (receipt string, resultErr error) {
	signer, err := ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
	if err != nil {
		return "", errors.New("invalid SSH private key")
	}
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(cfg.HostKey))
	if err != nil {
		return "", errors.New("invalid pinned SSH host key")
	}
	dialer := net.Dialer{Timeout: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return "", errors.New("SSH connection failed")
	}
	defer func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			resultErr = errors.Join(resultErr, errors.New("close SSH connection failed"))
		}
	}()
	deadline := time.Now().Add(2 * time.Minute)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return "", fmt.Errorf("set SSH deadline: %w", err)
	}
	stop := context.AfterFunc(ctx, func() {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return
		}
	})
	defer stop()
	connection, channels, requests, err := ssh.NewClientConn(conn, cfg.Address, &ssh.ClientConfig{User: cfg.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(hostKey), Timeout: 30 * time.Second})
	if err != nil {
		return "", errors.New("SSH authentication or host key verification failed")
	}
	client := ssh.NewClient(connection, channels, requests)
	defer func() {
		if err := client.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			resultErr = errors.Join(resultErr, errors.New("close SSH client failed"))
		}
	}()
	files, err := sftp.NewClient(client)
	if err != nil {
		return "", errors.New("SFTP initialization failed")
	}
	defer func() {
		if err := files.Close(); err != nil {
			resultErr = errors.Join(resultErr, errors.New("close SFTP client failed"))
		}
	}()
	if err := uploadFile(files, cfg.KeyPath, bundle.PrivateKey); err != nil {
		return "", err
	}
	if err := uploadFile(files, cfg.CertificatePath, bundle.Certificate); err != nil {
		return "", err
	}
	if cfg.ReloadCommand != "" {
		session, err := client.NewSession()
		if err != nil {
			return "", errors.New("SSH reload session failed")
		}
		defer func() {
			if err := session.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				return
			}
		}()
		if err := session.Run(cfg.ReloadCommand); err != nil {
			return "", errors.New("SSH reload command failed; files were uploaded")
		}
	}
	return bundle.Fingerprint, nil
}

func uploadFile(client *sftp.Client, target string, data []byte) (resultErr error) {
	temporary := target + ".certcenter-" + rand.Text()
	file, err := client.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return errors.New("cannot create remote certificate file")
	}
	defer func() {
		if err := client.Remove(temporary); err != nil && !os.IsNotExist(err) {
			resultErr = errors.Join(resultErr, errors.New("remote temporary file cleanup failed"))
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return errors.New("remote file chmod and close failed")
		}
		return errors.New("cannot restrict remote file permissions")
	}
	_, writeErr := file.ReadFrom(bytes.NewReader(data))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("remote certificate write failed")
	}
	if err := client.PosixRename(temporary, target); err != nil {
		return errors.New("atomic remote file replacement failed; SFTP posix-rename is required")
	}
	return nil
}
