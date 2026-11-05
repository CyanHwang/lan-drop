//go:build server

// Server mode: the same LAN transfer engine as the desktop app, with no GUI
// and no Wails dependency. Built with `-tags server`, it produces a single
// static binary suitable for a NAS, a Raspberry Pi or a container.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/CyanHwang/lan-drop/internal/config"
	"github.com/CyanHwang/lan-drop/internal/i18n"
	"github.com/CyanHwang/lan-drop/internal/transfer"
)

// shareList collects repeatable -share flags. A single flag may also carry a
// comma-separated list, which keeps compose files readable.
type shareList []string

func (s *shareList) String() string { return strings.Join(*s, ",") }

func (s *shareList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

func main() {
	// Help text follows the OS language: at -h time the config file may not
	// exist yet, so there is no stored preference to honour.
	helpLang := i18n.Detect()

	var (
		addr       = flag.String("addr", ":57577", i18n.T(helpLang, i18n.CliOptAddr))
		configPath = flag.String("config", "", i18n.T(helpLang, i18n.CliOptConfig))
		readOnly   = flag.Bool("readonly", false, i18n.T(helpLang, i18n.CliOptReadOnly))
		autoPort   = flag.Bool("auto-port", false, i18n.T(helpLang, i18n.CliOptAutoPort))
		shares     shareList
	)
	flag.Var(&shares, "share", i18n.T(helpLang, i18n.CliOptShare))
	flag.Usage = usage(helpLang)
	flag.Parse()

	cfg := config.LoadFrom(*configPath)
	lang := i18n.Resolve(cfg.Language)

	list := resolveShares(shares, cfg)
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, i18n.T(lang, i18n.CliNoShares))
		os.Exit(2)
	}

	host, port, err := splitAddr(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.T(lang, i18n.CliBadAddr, *addr, err))
		os.Exit(2)
	}

	srv := transfer.New(nil)
	info, err := srv.StartWithOptions(list, transfer.StartOptions{
		Port:         port,
		Host:         host,
		StrictPort:   !*autoPort,
		ReadOnly:     *readOnly,
		Lang:         lang,
		ShowHidden:   cfg.ShowHidden,
		ShowDotfiles: cfg.ShowDotfiles,
		Filter:       cfg.Filter,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	printBanner(lang, info, *readOnly)

	// Block until the process is asked to stop. systemd and Docker both send
	// SIGTERM; a Ctrl+C sends SIGINT. Either way the listener closes cleanly
	// so in-flight uploads fail fast instead of hanging on a dead socket.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	fmt.Println()
	fmt.Println(i18n.T(lang, i18n.CliStopping))
	if err := srv.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, i18n.T(lang, i18n.CliStopError, err))
		os.Exit(1)
	}
}

func resolveShares(flags shareList, cfg config.Config) []transfer.Share {
	if len(flags) == 0 {
		return cfg.Shares
	}
	out := make([]transfer.Share, 0, len(flags))
	for _, p := range flags {
		out = append(out, shareFor(p))
	}
	return out
}

func shareFor(path string) transfer.Share {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return transfer.Share{ID: transfer.ShareID(abs), Name: filepath.Base(abs), Path: abs}
}

// splitAddr accepts ":57577", "127.0.0.1:57577" and a bare "57577".
func splitAddr(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		if n, convErr := strconv.Atoi(addr); convErr == nil {
			portStr, host = strconv.Itoa(n), ""
		} else {
			return "", 0, err
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("port %q is not a number", portStr)
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("port %d is out of range 1-65535", port)
	}
	return host, port, nil
}

func printBanner(lang i18n.Lang, info transfer.Info, readOnly bool) {
	mode := i18n.T(lang, i18n.CliModeReadWrite)
	if readOnly {
		mode = i18n.T(lang, i18n.CliModeReadOnly)
	}
	fmt.Println(i18n.T(lang, i18n.CliBannerTitle, mode))
	fmt.Println()

	fmt.Println(i18n.T(lang, i18n.CliShares, len(info.Shares)))
	for _, sh := range info.Shares {
		fmt.Printf("  %-18s %s\n", sh.Name, sh.Path)
	}

	fmt.Println()
	fmt.Println(i18n.T(lang, i18n.CliURLs))
	fmt.Printf("  http://127.0.0.1:%d\n", info.Port)
	for _, u := range info.URLs {
		fmt.Printf("  %s\n", u)
	}

	fmt.Println()
	fmt.Println(i18n.T(lang, i18n.CliStopHint))
}

func usage(lang i18n.Lang) func() {
	return func() {
		out := flag.CommandLine.Output()
		name := filepath.Base(os.Args[0])

		fmt.Fprintln(out, i18n.T(lang, i18n.CliUsageTitle))
		fmt.Fprintln(out)
		fmt.Fprintln(out, i18n.T(lang, i18n.CliUsageLine, name))
		fmt.Fprintln(out)
		fmt.Fprintln(out, i18n.T(lang, i18n.CliOptsTitle))
		flag.PrintDefaults()
		fmt.Fprintln(out)
		fmt.Fprintln(out, i18n.T(lang, i18n.CliExample, name, name))
	}
}
