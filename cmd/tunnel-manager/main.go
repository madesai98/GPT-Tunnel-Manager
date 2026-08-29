package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	coreapp "github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/buildinfo"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/instance"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/portable"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/v2config"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	root, err := portable.Resolve(exe)
	if err != nil {
		return err
	}

	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-version":
			fmt.Println(buildinfo.Version)
			return nil
		case "print-root":
			fmt.Println(root)
			return nil
		case "init":
			if err := portable.EnsureWritable(root); err != nil {
				return err
			}
			_, _, err := v2config.NewStore(root).LoadOrCreate()
			return err
		case "validate":
			_, _, err := v2config.NewStore(root).LoadOrCreate()
			if err == nil {
				fmt.Println("configuration valid")
			}
			return err
		case "secret":
			return secretCommand(root, args[1:])
		}
	}

	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	rootFlag := flags.String("root", "", "override Portable Root (development/testing)")
	noGUI := flags.Bool("no-gui", false, "run without the native desktop UI")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *rootFlag != "" {
		root = *rootFlag
	}
	if err := portable.EnsureWritable(root); err != nil {
		return err
	}

	owner, err := instance.Acquire(root)
	if err != nil {
		if errors.Is(err, instance.ErrAlreadyRunning) {
			fmt.Println("GPT Tunnel Manager is already running.")
			return nil
		}
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = owner.Close(ctx)
		cancel()
	}()

	application, err := coreapp.NewV2App(context.Background(), root)
	if err != nil {
		return err
	}
	defer application.Close()
	if err := application.Start(context.Background()); err != nil {
		return err
	}
	if *noGUI {
		fmt.Println("GPT Tunnel Manager MCP:", application.ManagerSnapshot().MCPURL)
		return runHeadless(application)
	}
	return runDesktopV2(application, owner.SetFocus)
}

func runHeadless(application *coreapp.V2App) error {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-signals:
		application.RequestShutdown()
	case <-application.Done():
	}
	<-application.Done()
	return nil
}

func secretCommand(root string, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: tunnel-manager secret <put|delete|get> <secret://ref>")
	}
	store := secrets.New(root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch args[0] {
	case "put":
		reader := bufio.NewReader(io.LimitReader(os.Stdin, 1<<20))
		value, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		value = []byte(strings.TrimRight(string(value), "\r\n"))
		return store.Put(ctx, args[1], value)
	case "delete":
		return store.Delete(ctx, args[1])
	case "get":
		value, err := store.Get(ctx, args[1])
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(value, '\n'))
		return err
	default:
		return errors.New("usage: tunnel-manager secret <put|delete|get> <secret://ref>")
	}
}
