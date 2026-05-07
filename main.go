package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

// Version is the application version, it can be set at compile time
var Version = "dev"

type command struct {
	help           bool
	displayVersion bool
	name           string
	target         string
	output         string
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

func displayHelp() {
	fmt.Println(`Usage:
   check [url]
   check curl <url>
   check wget [-O output] <url>

   Example:
   check tcp://example.com:2222
   check http://example.com:8080
   check https://example.com:8443
   check curl https://example.com/health
   check wget -O check.deb https://example.com/check.deb

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
   wget -O <file> download a URL to the specified file`)
}
