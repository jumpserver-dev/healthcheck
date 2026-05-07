package main

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// Version is the application version, it can be set at compile time
var Version = "dev"

type command struct {
	help           bool
	displayVersion bool
	name           string
	target         string
	output         string
	filter         string
}

func main() {
	cmd := parseArgs(os.Args)
	if cmd.help {
		displayHelp()
		os.Exit(0)
	}

	if cmd.displayVersion {
		fmt.Println("Version:", Version)
		os.Exit(0)
	}

	err := runCommand(cmd)
	if err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}

func parseArgs(args []string) command {
	cmd := command{name: "check"}
	rest := make([]string, 0, len(args))

	for _, arg := range args[1:] {
		switch arg {
		case "-h", "--help":
			cmd.help = true
		case "-v", "--version":
			cmd.displayVersion = true
		default:
			rest = append(rest, arg)
		}
	}

	if len(rest) == 0 {
		return cmd
	}

	switch rest[0] {
	case "curl":
		cmd.name = "curl"
		if len(rest) != 2 {
			fmt.Println("Usage: check curl <url>")
			os.Exit(1)
		}
		cmd.target = rest[1]
	case "wget":
		cmd.name = "wget"
		parseWgetArgs(&cmd, rest[1:])
	case "ping":
		cmd.name = "ping"
		if len(rest) != 2 {
			fmt.Println("Usage: check ping <host>")
			os.Exit(1)
		}
		cmd.target = rest[1]
	case "ps":
		cmd.name = "ps"
		if len(rest) > 2 {
			fmt.Println("Usage: check ps [pattern]")
			os.Exit(1)
		}
		if len(rest) == 2 {
			cmd.filter = rest[1]
		}
	default:
		if len(rest) != 1 {
			fmt.Println("Multiple targets specified")
			os.Exit(1)
		}
		cmd.target = rest[0]
	}

	return cmd
}

func parseWgetArgs(cmd *command, args []string) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-O":
			if i+1 >= len(args) {
				fmt.Println("Usage: check wget [-O output] <url>")
				os.Exit(1)
			}
			cmd.output = args[i+1]
			i++
		default:
			if cmd.target != "" {
				fmt.Println("Usage: check wget [-O output] <url>")
				os.Exit(1)
			}
			cmd.target = args[i]
		}
	}

	if cmd.target == "" {
		fmt.Println("Usage: check wget [-O output] <url>")
		os.Exit(1)
	}
}

func runCommand(cmd command) error {
	switch cmd.name {
	case "curl":
		return curlURL(cmd.target, os.Stdout)
	case "wget":
		return downloadURL(cmd.target, cmd.output)
	case "ping":
		return pingHost(cmd.target, os.Stdout)
	case "ps":
		return listProcesses(cmd.filter, os.Stdout)
	default:
		return checkTarget(cmd.target)
	}
}

func checkTarget(target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return fmt.Errorf("%v", err)
	}

	switch u.Scheme {
	case "http", "https":
		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
		resp, err := client.Get(target)
		if err != nil {
			return fmt.Errorf("%v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("get %s %d", target, resp.StatusCode)
		}
	case "tcp":
		conn, err := net.Dial("tcp", u.Host)
		if err != nil {
			return fmt.Errorf("%v", err)
		}
		conn.Close()
	default:
		return fmt.Errorf("invalid check type")
	}

	return nil
}

func curlURL(target string, output io.Writer) error {
	resp, err := httpGet(target)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("get %s %d", target, resp.StatusCode)
	}

	_, err = io.Copy(output, resp.Body)
	return err
}

func downloadURL(target string, output string) error {
	resp, err := httpGet(target)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("get %s %d", target, resp.StatusCode)
	}

	if output == "" {
		output, err = defaultDownloadPath(target)
		if err != nil {
			return err
		}
	}

	file, err := os.Create(output)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err
}

func httpGet(target string) (*http.Response, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("%v", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid download type")
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Get(target)
	if err != nil {
		return nil, fmt.Errorf("%v", err)
	}

	return resp, nil
}

func defaultDownloadPath(target string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("%v", err)
	}

	base := path.Base(u.Path)
	if base == "." || base == "/" || base == "" {
		return "", fmt.Errorf("output file required for %s", target)
	}

	return strings.TrimSpace(base), nil
}

func pingHost(target string, output io.Writer) error {
	ip, network, err := resolvePingTarget(target)
	if err != nil {
		return err
	}

	conn, protocol, err := openPingConn(network)
	if err != nil {
		return err
	}
	defer conn.Close()

	echoID := os.Getpid() & 0xffff
	echoSeq := 1
	wm, err := marshalPingMessage(network, echoID, echoSeq)
	if err != nil {
		return err
	}

	start := time.Now()
	if err := conn.SetDeadline(start.Add(3 * time.Second)); err != nil {
		return err
	}
	if _, err := conn.WriteTo(wm, ip); err != nil {
		return err
	}

	reply := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(reply)
		if err != nil {
			return err
		}

		ok, err := isExpectedPingReply(network, protocol, reply[:n], echoID, echoSeq)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}

		rtt := time.Since(start).Milliseconds()
		fmt.Fprintf(output, "PING %s (%s): ok time=%dms from %s\n", target, ip.IP.String(), rtt, peer.String())
		return nil
	}
}

func resolvePingTarget(target string) (*net.IPAddr, string, error) {
	ip, err := net.ResolveIPAddr("ip", target)
	if err != nil {
		return nil, "", err
	}
	if ip.IP.To4() != nil {
		return ip, "ip4", nil
	}
	if ip.IP.To16() != nil {
		return ip, "ip6", nil
	}
	return nil, "", fmt.Errorf("invalid host: %s", target)
}

func openPingConn(network string) (*icmp.PacketConn, int, error) {
	switch network {
	case "ip4":
		conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
		return conn, 1, err
	case "ip6":
		conn, err := icmp.ListenPacket("udp6", "::")
		return conn, 58, err
	default:
		return nil, 0, fmt.Errorf("unsupported network: %s", network)
	}
}

func marshalPingMessage(network string, id int, seq int) ([]byte, error) {
	var typ icmp.Type
	if network == "ip4" {
		typ = ipv4.ICMPTypeEcho
	} else {
		typ = ipv6.ICMPTypeEchoRequest
	}

	msg := icmp.Message{
		Type: typ,
		Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  seq,
			Data: marshalPingData(),
		},
	}

	return msg.Marshal(nil)
}

func marshalPingData() []byte {
	data := make([]byte, 8)
	binary.BigEndian.PutUint64(data, uint64(time.Now().UnixNano()))
	return data
}

func isExpectedPingReply(network string, protocol int, data []byte, id int, seq int) (bool, error) {
	msg, err := icmp.ParseMessage(protocol, data)
	if err != nil {
		return false, err
	}

	switch body := msg.Body.(type) {
	case *icmp.Echo:
		if body.ID != id || body.Seq != seq {
			return false, nil
		}
		if network == "ip4" && msg.Type == ipv4.ICMPTypeEchoReply {
			return true, nil
		}
		if network == "ip6" && msg.Type == ipv6.ICMPTypeEchoReply {
			return true, nil
		}
	}

	return false, nil
}

type processInfo struct {
	pid     int
	command string
}

func listProcesses(filter string, output io.Writer) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("ps is only supported on linux")
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}

	processes := make([]processInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		command, err := readProcessCommand(entry.Name())
		if err != nil {
			if isIgnorableProcError(err) {
				continue
			}
			return err
		}
		if filter != "" && !strings.Contains(command, filter) {
			continue
		}

		processes = append(processes, processInfo{pid: pid, command: command})
	}

	sort.Slice(processes, func(i, j int) bool {
		return processes[i].pid < processes[j].pid
	})

	fmt.Fprintln(output, "PID COMMAND")
	for _, process := range processes {
		fmt.Fprintf(output, "%d %s\n", process.pid, process.command)
	}

	return nil
}

func readProcessCommand(pid string) (string, error) {
	cmdlinePath := filepath.Join("/proc", pid, "cmdline")
	data, err := os.ReadFile(cmdlinePath)
	if err == nil {
		command := strings.ReplaceAll(string(data), "\x00", " ")
		command = strings.TrimSpace(command)
		if command != "" {
			return command, nil
		}
	}
	if err != nil && !os.IsNotExist(err) && !os.IsPermission(err) {
		return "", err
	}

	commPath := filepath.Join("/proc", pid, "comm")
	data, err = os.ReadFile(commPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func isIgnorableProcError(err error) bool {
	return os.IsNotExist(err) || os.IsPermission(err) || errorsIsInvalidArgument(err)
}

func errorsIsInvalidArgument(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid argument")
}

func displayHelp() {
	fmt.Println(`Usage:
   check [url]
   check curl <url>
   check wget [-O output] <url>
   check ping <host>
   check ps [pattern]

   Example:
   check tcp://example.com:2222
   check http://example.com:8080
   check https://example.com:8443
   check curl https://example.com/health
   check wget -O check.deb https://example.com/check.deb
   check ping 127.0.0.1
   check ps check

Version:
   ` + Version + `

Description:
   check is a high performance check tool whose command can be started
   by using this command. It supports health checks and simple HTTP download helpers.

Global Options:
   --help         show help
   --version      print the version

Commands:
   [url]          run a health check for http(s) or tcp targets
   curl <url>     fetch a URL and print the response body
   wget <url>     download a URL to a file
   wget -O <file> download a URL to the specified file
   ping <host>    send one ICMP echo request
   ps [pattern]   list linux processes, optionally filtered by keyword`)
}
