package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mashfromband/mashfromband/internal/activity"
)

func load(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return activity.Decode(b, out)
}
func atomicWrite(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".profile-activity-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func run(args []string) error {
	fs := flag.NewFlagSet("profile-activity", flag.ContinueOnError)
	mode := fs.String("mode", "check", "check, render, collect or sync")
	hours := fs.String("review-hours", "168", "createdAt grace period; manual acceptance may use 0")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected argument")
	}
	var cfg activity.Config
	if err := load(activity.ConfigPath, &cfg); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	// Deliberately never read PROFILE_STATS_TOKEN or GH_TOKEN here.
	api := activity.NewClient(os.Getenv("GITHUB_TOKEN"))
	if *mode == "sync" {
		if api.Token == "" {
			return errors.New("sync requires the profile workflow GITHUB_TOKEN")
		}
		delay, err := activity.ParseHours(*hours)
		if err != nil {
			return err
		}
		if delay != 168*time.Hour && os.Getenv("GITHUB_EVENT_NAME") != "workflow_dispatch" {
			return errors.New("a shortened grace period is only allowed for explicit manual acceptance")
		}
		result, err := activity.Sync(ctx, cfg, api, time.Now().UTC(), delay)
		_ = json.NewEncoder(os.Stdout).Encode(result)
		if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
			if f, e := os.OpenFile(summary, os.O_APPEND|os.O_WRONLY, 0600); e == nil {
				fmt.Fprintf(f, "Public activity: %s; PR #%d; %d entries; HEAD %s\n", result.Outcome, result.PR, result.Entries, result.Head)
				f.Close()
			}
		}
		return err
	}
	var old activity.Data
	var excluded []activity.Exclusion
	if err := load(activity.DataPath, &old); err != nil {
		return err
	}
	if err := load(activity.ExclusionsPath, &excluded); err != nil {
		return err
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		return err
	}
	if *mode == "check" {
		return activity.Check(cfg, old, excluded, string(readme))
	}
	if *mode != "collect" && *mode != "render" {
		return errors.New("unknown mode")
	}
	var fresh []activity.Entry
	if *mode == "collect" {
		fresh, err = cfg.Collect(ctx, api)
		if err != nil {
			return err
		}
	}
	next, text, err := activity.Build(cfg, old, excluded, string(readme), fresh, time.Now().UTC())
	if err != nil {
		return err
	}
	// Fetching, parsing and validation finish before either output is touched.
	// If the second filesystem replacement fails, restore the first output.
	prior, err := os.ReadFile(activity.DataPath)
	if err != nil {
		return err
	}
	if err = atomicWrite(activity.DataPath, activity.Encode(next)); err != nil {
		return err
	}
	if err = atomicWrite("README.md", []byte(text)); err != nil {
		if restore := atomicWrite(activity.DataPath, prior); restore != nil {
			return fmt.Errorf("README write and data restoration both failed")
		}
		return err
	}
	fmt.Printf("Collected %d public activities\n", len(next.Entries))
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Public activity update failed:", err)
		os.Exit(1)
	}
}
