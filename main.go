package main

import (
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println(version)
		return
	}

	final, err := tea.NewProgram(NewModel()).Run()
	if err != nil {
		log.Fatal(err)
	}

	if m, ok := final.(Model); ok && m.err != nil {
		log.Fatal(m.err)
	}
}
