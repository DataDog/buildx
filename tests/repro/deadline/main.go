package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/util/grpcerrors"
	"google.golang.org/grpc/codes"
)

func main() {
	addr := flag.String("addr", "", "local BuildKit Unix socket")
	fixture := flag.String("fixture", "", "path to static offline fixture")
	mode := flag.String("mode", "sleep", "fixture mode: ok or sleep")
	flag.Parse()
	if !strings.HasPrefix(*addr, "unix://") {
		panic("local Unix socket required")
	}
	binary, err := os.ReadFile(*fixture)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	c, err := client.New(ctx, *addr)
	if err != nil {
		panic(err)
	}
	defer c.Close()
	state := llb.Scratch().File(llb.Mkfile("/repro", 0755, binary)).Run(llb.Args([]string{"/repro", *mode}), llb.IgnoreCache).Root()
	def, err := state.Marshal(ctx)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	progress := make(chan *client.SolveStatus)
	done := make(chan struct{})
	started := false
	go func() {
		defer close(done)
		for s := range progress {
			for _, l := range s.Logs {
				text := string(l.Data)
				fmt.Fprint(os.Stderr, text)
				if strings.Contains(text, "REPRO_STARTED") {
					started = true
				}
			}
		}
	}()
	start := time.Now()
	_, err = c.Solve(ctx, def, client.SolveOpt{}, progress)
	<-done
	message := ""
	if err != nil {
		message = err.Error()
	}
	result := map[string]any{"mode": *mode, "started": started, "deadline_exceeded": grpcerrors.Code(err) == codes.DeadlineExceeded, "error": message, "elapsed_seconds": time.Since(start).Seconds()}
	json.NewEncoder(os.Stdout).Encode(result)
	if !started || (*mode == "ok" && err != nil) || (*mode == "sleep" && grpcerrors.Code(err) != codes.DeadlineExceeded) {
		os.Exit(1)
	}
}
