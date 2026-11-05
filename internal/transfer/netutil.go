package transfer

import (
	"net"
	"sort"
	"strconv"
	"strings"
)

// DefaultPort is the first port tried when starting the listener.
const DefaultPort = 57577

// listenFree binds host:preferred. When that port is taken it walks forward up
// to 100 ports, then falls back to an OS-assigned one. Port conflicts are the
// second most common reason a phone cannot connect, after the Windows firewall
// prompt — but a container or reverse proxy needs the exact port, so strict
// mode fails loudly instead.
func listenFree(preferred int, host string, strict bool) (net.Listener, int, error) {
	if preferred <= 0 {
		preferred = DefaultPort
	}
	if host == "" {
		host = "0.0.0.0"
	}

	addr := func(p int) string { return net.JoinHostPort(host, strconv.Itoa(p)) }

	if strict {
		ln, err := net.Listen("tcp", addr(preferred))
		if err != nil {
			return nil, 0, err
		}
		return ln, preferred, nil
	}

	for p := preferred; p < preferred+100 && p <= 65535; p++ {
		ln, err := net.Listen("tcp", addr(p))
		if err == nil {
			return ln, p, nil
		}
	}
	ln, err := net.Listen("tcp", addr(0))
	if err != nil {
		return nil, 0, err
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

type ipCandidate struct {
	ip    string
	score int
}

// LANIPs returns private IPv4 addresses this machine can be reached on,
// best candidate first. Virtual adapters (VMware, Hyper-V, VirtualBox, WSL)
// are ranked last so the UI shows the Wi-Fi/Ethernet address by default.
func LANIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var cands []ipCandidate
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(ifi.Name)
		virtual := isVirtualAdapter(name)

		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || !ip4.IsPrivate() {
				continue
			}

			score := 100
			switch {
			case ip4[0] == 192 && ip4[1] == 168:
				score += 30
			case ip4[0] == 10:
				score += 20
			case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
				score += 10
			}
			if virtual {
				score -= 80
			}
			cands = append(cands, ipCandidate{ip: ip4.String(), score: score})
		}
	}

	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })

	out := make([]string, 0, len(cands))
	seen := map[string]bool{}
	for _, c := range cands {
		if seen[c.ip] {
			continue
		}
		seen[c.ip] = true
		out = append(out, c.ip)
	}
	return out
}

func isVirtualAdapter(name string) bool {
	// Covers the usual suspects on all three platforms: Hyper-V and VMware on
	// Windows, bridges and container bridges on Linux, and the utun/awdl
	// pseudo-interfaces macOS creates for VPNs and AirDrop.
	markers := []string{
		"vmware", "virtualbox", "vbox", "hyper-v", "vethernet",
		"loopback", "tap", "tun", "wsl", "docker", "br-", "veth",
		"utun", "awdl", "llw", "anpi",
		"zerotier", "tailscale", "radmin", "hamachi",
	}
	for _, m := range markers {
		if strings.Contains(name, m) {
			return true
		}
	}
	return false
}
