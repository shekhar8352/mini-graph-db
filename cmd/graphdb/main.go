package main

import (
	"flag"
	"fmt"
	"os"

	"mini-graph-db/internal/repl"
)

func main() {
	dbPath := flag.String("db", "graph.db", "snapshot file loaded on startup and used by SAVE/LOAD defaults")
	walPath := flag.String("wal", "graph.wal", "append-only write-ahead log")
	histPath := flag.String("history", "graph.history", "REPL command history")
	flag.Parse()

	if err := repl.Run(repl.Config{
		DBPath:      *dbPath,
		WALPath:     *walPath,
		HistoryPath: *histPath,
		In:          os.Stdin,
		Out:         os.Stdout,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
