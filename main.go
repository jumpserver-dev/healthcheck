package main

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
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
	case "netstat":
		cmd.name = "netstat"
		if len(rest) > 2 || (len(rest) == 2 && rest[1] != "-tulnp") {
			fmt.Println("Usage: check netstat [-tulnp]")
			os.Exit(1)
		}
	case "edit", "vi", "vim":
		cmd.name = "edit"
		if len(rest) != 2 {
			fmt.Println("Usage: check edit|vi|vim <file>")
			os.Exit(1)
		}
		cmd.target = rest[1]
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
	case "netstat":
		return listListeningPorts(os.Stdout)
	case "edit":
		return editFile(cmd.target)
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

	conn, protocol, privileged, err := openPingConn(network)
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
	dst := pingDestinationAddr(ip, privileged)
	if _, err := conn.WriteTo(wm, dst); err != nil {
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

func openPingConn(network string) (*icmp.PacketConn, int, bool, error) {
	switch network {
	case "ip4":
		if conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
			return conn, 1, true, nil
		}
		conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
		return conn, 1, false, err
	case "ip6":
		if conn, err := icmp.ListenPacket("ip6:ipv6-icmp", "::"); err == nil {
			return conn, 58, true, nil
		}
		conn, err := icmp.ListenPacket("udp6", "::")
		return conn, 58, false, err
	default:
		return nil, 0, false, fmt.Errorf("unsupported network: %s", network)
	}
}

func pingDestinationAddr(ip *net.IPAddr, privileged bool) net.Addr {
	if privileged {
		return ip
	}

	return &net.UDPAddr{
		IP:   ip.IP,
		Zone: ip.Zone,
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

type listeningPort struct {
	proto   string
	address string
	port    int
	state   string
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

func listListeningPorts(output io.Writer) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("netstat is only supported on linux")
	}

	processesByInode, err := socketProcessesByInode()
	if err != nil {
		return err
	}

	files := []struct {
		path  string
		proto string
		ipv6  bool
	}{
		{path: "/proc/net/tcp", proto: "tcp", ipv6: false},
		{path: "/proc/net/tcp6", proto: "tcp6", ipv6: true},
		{path: "/proc/net/udp", proto: "udp", ipv6: false},
		{path: "/proc/net/udp6", proto: "udp6", ipv6: true},
	}

	ports := make([]listeningPort, 0)
	for _, file := range files {
		current, err := readListeningPorts(file.path, file.proto, file.ipv6, processesByInode)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		ports = append(ports, current...)
	}

	sort.Slice(ports, func(i, j int) bool {
		if ports[i].proto != ports[j].proto {
			return ports[i].proto < ports[j].proto
		}
		if ports[i].port != ports[j].port {
			return ports[i].port < ports[j].port
		}
		return ports[i].address < ports[j].address
	})

	fmt.Fprintln(output, "Proto Recv-Q Send-Q Local Address           Foreign Address         State       PID/Program name")
	for _, port := range ports {
		process := "-"
		if port.pid > 0 || port.command != "" {
			process = fmt.Sprintf("%d/%s", port.pid, port.command)
		}
		state := port.state
		if state == "" {
			state = "-"
		}
		fmt.Fprintf(output, "%-5s %-6d %-6d %-23s %-23s %-11s %s\n",
			port.proto, 0, 0, port.address, "*:*", state, process)
	}

	return nil
}

func readListeningPorts(path string, proto string, ipv6 bool, processesByInode map[string]processInfo) ([]listeningPort, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	ports := make([]listeningPort, 0, len(lines))
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		state := fields[3]
		if strings.HasPrefix(proto, "tcp") && state != "0A" {
			continue
		}

		address, port, err := parseProcNetAddress(fields[1], ipv6)
		if err != nil {
			continue
		}

		process := processesByInode[fields[9]]
		ports = append(ports, listeningPort{
			proto:   proto,
			address: net.JoinHostPort(address, strconv.Itoa(port)),
			port:    port,
			state:   socketStateName(state, proto),
			pid:     process.pid,
			command: process.command,
		})
	}

	return ports, nil
}

func socketProcessesByInode() (map[string]processInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	processesByInode := make(map[string]processInfo)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		fdPath := filepath.Join("/proc", entry.Name(), "fd")
		fds, err := os.ReadDir(fdPath)
		if err != nil {
			if isIgnorableProcError(err) {
				continue
			}
			return nil, err
		}

		var command string
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdPath, fd.Name()))
			if err != nil {
				if isIgnorableProcError(err) {
					continue
				}
				return nil, err
			}
			inode, ok := parseSocketInode(target)
			if !ok {
				continue
			}
			if command == "" {
				command, err = readProcessName(entry.Name())
				if err != nil {
					if isIgnorableProcError(err) {
						command = "-"
					} else {
						return nil, err
					}
				}
			}
			processesByInode[inode] = processInfo{pid: pid, command: command}
		}
	}

	return processesByInode, nil
}

func parseSocketInode(target string) (string, bool) {
	if !strings.HasPrefix(target, "socket:[") || !strings.HasSuffix(target, "]") {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]"), true
}

func readProcessName(pid string) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", pid, "comm"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func parseProcNetAddress(value string, ipv6 bool) (string, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid address: %s", value)
	}

	port64, err := strconv.ParseUint(parts[1], 16, 16)
	if err != nil {
		return "", 0, err
	}

	if ipv6 {
		ip, err := parseProcNetIPv6(parts[0])
		if err != nil {
			return "", 0, err
		}
		return ip.String(), int(port64), nil
	}

	ip, err := parseProcNetIPv4(parts[0])
	if err != nil {
		return "", 0, err
	}
	return ip.String(), int(port64), nil
}

func parseProcNetIPv4(value string) (net.IP, error) {
	raw, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return nil, err
	}
	return net.IPv4(byte(raw), byte(raw>>8), byte(raw>>16), byte(raw>>24)), nil
}

func parseProcNetIPv6(value string) (net.IP, error) {
	if len(value) != 32 {
		return nil, fmt.Errorf("invalid ipv6 address: %s", value)
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+3] = raw[i+3], raw[i]
		raw[i+1], raw[i+2] = raw[i+2], raw[i+1]
	}
	return net.IP(raw), nil
}

func socketStateName(state string, proto string) string {
	if strings.HasPrefix(proto, "udp") {
		return ""
	}
	switch state {
	case "0A":
		return "LISTEN"
	default:
		return state
	}
}

func displayHelp() {
	fmt.Println(`Usage:
   check [url]
   check curl <url>
   check wget [-O output] <url>
   check ping <host>
   check ps [pattern]
   check netstat [-tulnp]
   check edit <file>
   check vi <file>
   check vim <file>

   Example:
   check tcp://example.com:2222
   check http://example.com:8080
   check https://example.com:8443
   check curl https://example.com/health
   check wget -O check.deb https://example.com/check.deb
   check ping 127.0.0.1
   check ps check
   check netstat -tulnp
   check edit /etc/hosts
   check vi /etc/hosts

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
   ps [pattern]   list linux processes, optionally filtered by keyword
   netstat        list linux TCP/UDP listening ports and owning processes
   edit <file>    edit a text file interactively
   vi <file>      edit a text file interactively
   vim <file>     edit a text file interactively`)
}
