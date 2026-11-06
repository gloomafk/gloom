// Command gloom is a terminal IRC client.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"gloom.im/gloom/internal/irc"
	"gloom.im/gloom/internal/ui"
	"gloom.im/gloom/internal/version"
)

func main() { os.Exit(run()) }

// run returns the process exit status, so deferred cleanup runs before exit.
func run() int {
	server := flag.String("server", "", "IRC server (start disconnected when omitted)")
	port := flag.String("port", "", "IRC port (6667 TCP, 6697 TLS)")
	nick := flag.String("nick", "", "nickname (needed before connecting)")
	secure := flag.Bool("tls", false, "connect with verified TLS")
	light := flag.Bool("light", false, "use the light palette")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Name)
		return 0
	}
	if flag.NArg() != 0 || (*nick != "" && !irc.ValidNick(*nick)) {
		fmt.Fprintln(os.Stderr, "gloom: invalid arguments or nickname")
		return 2
	}
	m := ui.New(*nick, *light)
	defer m.Close()
	if *server != "" {
		if *nick == "" {
			fmt.Fprintln(os.Stderr, "gloom: -server needs -nick")
			return 2
		}
		c, err := irc.NewConfig(*server, *port, *secure, *nick)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		m.Start = &c
	}
	_, err := tea.NewProgram(m).Run()
	// gloom asked for the arrow pointer while running. Give the shell back the
	// terminal's usual pointer over text.
	fmt.Fprint(os.Stdout, ansi.SetPointerShape("text"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gloom:", err)
		return 1
	}
	return 0
}
