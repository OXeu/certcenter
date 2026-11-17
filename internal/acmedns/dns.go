package acmedns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/OXeu/certcenter/internal/store"
	"github.com/miekg/dns"
)

// Handler provides authoritative answers without recursion or public mutation endpoints.
func (s *Service) Handler(ctx context.Context, log *slog.Logger) dns.Handler {
	return dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		response := s.answer(queryCtx, r)
		if w.RemoteAddr().Network() != "tcp" {
			size := uint16(512)
			if opt := r.IsEdns0(); opt != nil {
				size = max(512, min(1232, opt.UDPSize()))
			}
			response.Truncate(int(size))
		}
		if err := w.WriteMsg(response); err != nil {
			log.Debug("write DNS answer failed", "error", err)
		}
	})
}

func (s *Service) soa() *dns.SOA {
	return &dns.SOA{Hdr: dns.RR_Header{Name: dns.Fqdn(s.Config.Zone), Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 30}, Ns: dns.Fqdn(s.Config.Nameserver), Mbox: "hostmaster." + dns.Fqdn(s.Config.Zone), Serial: 1, Refresh: 300, Retry: 60, Expire: 86400, Minttl: 30}
}

func (s *Service) answer(ctx context.Context, r *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	if r.Opcode != dns.OpcodeQuery || len(r.Question) != 1 || r.Question[0].Qclass != dns.ClassINET || r.Question[0].Qtype == dns.TypeAXFR || r.Question[0].Qtype == dns.TypeIXFR {
		m.Rcode = dns.RcodeRefused
		return m
	}
	q := r.Question[0]
	name := strings.ToLower(q.Name)
	zone := dns.Fqdn(s.Config.Zone)
	if !dns.IsSubDomain(zone, name) {
		m.Rcode = dns.RcodeRefused
		return m
	}
	m.Authoritative = true
	header := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Ttl: 30}
	if name == zone {
		switch q.Qtype {
		case dns.TypeSOA:
			m.Answer = append(m.Answer, s.soa())
		case dns.TypeNS:
			header.Rrtype = dns.TypeNS
			m.Answer = append(m.Answer, &dns.NS{Hdr: header, Ns: dns.Fqdn(s.Config.Nameserver)})
		}
	}
	if name == dns.Fqdn(s.Config.Nameserver) {
		for _, address := range s.Config.Addresses {
			ip := net.ParseIP(address)
			if ip.To4() != nil && q.Qtype == dns.TypeA {
				header.Rrtype = dns.TypeA
				m.Answer = append(m.Answer, &dns.A{Hdr: header, A: ip.To4()})
			} else if ip.To4() == nil && q.Qtype == dns.TypeAAAA {
				header.Rrtype = dns.TypeAAAA
				m.Answer = append(m.Answer, &dns.AAAA{Hdr: header, AAAA: ip})
			}
		}
	} else if name != zone {
		label := strings.TrimSuffix(name, "."+zone)
		d, err := s.Store.DNSDelegationByLabel(ctx, label)
		switch {
		case errors.Is(err, store.ErrNotFound):
			m.Rcode = dns.RcodeNameError
		case err != nil:
			m.Rcode = dns.RcodeServerFailure
		case q.Qtype == dns.TypeTXT:
			values, err := s.Store.DNSChallengeValues(ctx, d.ID)
			if err != nil {
				m.Rcode = dns.RcodeServerFailure
				break
			}
			header.Rrtype = dns.TypeTXT
			for _, value := range values {
				m.Answer = append(m.Answer, &dns.TXT{Hdr: header, Txt: []string{value}})
			}
		}
	}
	if len(m.Answer) == 0 && m.Rcode != dns.RcodeServerFailure {
		m.Ns = append(m.Ns, s.soa())
	}
	return m
}

// Start binds TCP and UDP before returning, then shuts down both when the process context ends.
func (s *Service) Start(ctx context.Context, log *slog.Logger) error {
	if err := s.Config.Validate(); err != nil {
		return fmt.Errorf("configure DNS listener: %w", err)
	}
	if s.Config.Zone == "" {
		return errors.New("acme_dns zone is not configured")
	}
	var listen net.ListenConfig
	udp, err := listen.ListenPacket(ctx, "udp", s.Config.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen for UDP DNS: %w", err)
	}
	tcp, err := listen.Listen(ctx, "tcp", udp.LocalAddr().String())
	if err != nil {
		if closeErr := udp.Close(); closeErr != nil {
			return fmt.Errorf("listen for TCP DNS: %w", errors.Join(err, closeErr))
		}
		return fmt.Errorf("listen for TCP DNS: %w", err)
	}
	s.Config.ListenAddress = udp.LocalAddr().String()
	handler := s.Handler(ctx, log)
	servers := []*dns.Server{{PacketConn: udp, Handler: handler}, {Listener: tcp, Handler: handler}}
	for _, server := range servers {
		ready := make(chan struct{})
		server.NotifyStartedFunc = func() { close(ready) }
		done := make(chan error, 1)
		go func() {
			err := server.ActivateAndServe()
			done <- err
			if err != nil && ctx.Err() == nil {
				log.Error("authoritative DNS stopped", "error", err)
			}
		}()
		select {
		case <-ready:
		case err := <-done:
			return fmt.Errorf("start authoritative DNS: %w", errors.Join(errors.New("listener stopped before ready"), err, closeSockets(udp, tcp)))
		case <-ctx.Done():
			return fmt.Errorf("start authoritative DNS: %w", errors.Join(ctx.Err(), closeSockets(udp, tcp)))
		}
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		for _, server := range servers {
			if err := server.ShutdownContext(shutdownCtx); err != nil {
				log.Error("shutdown authoritative DNS", "error", err)
			}
		}
	}()
	log.Info("authoritative DNS listening", "address", udp.LocalAddr().String(), "zone", s.Config.Zone)
	return nil
}

func closeSockets(udp net.PacketConn, tcp net.Listener) error {
	udpErr, tcpErr := udp.Close(), tcp.Close()
	if errors.Is(udpErr, net.ErrClosed) {
		udpErr = nil
	}
	if errors.Is(tcpErr, net.ErrClosed) {
		tcpErr = nil
	}
	return errors.Join(udpErr, tcpErr)
}
