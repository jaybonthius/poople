// Adapted from Northstar and Northstar Pro.
/*
Copyright (c) 2025 Nicholas Zanghi

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/evanw/esbuild/pkg/api"
)

func main() {
	live := flag.Bool("live", false, "watch assets and reload open tabs")
	dev := flag.Bool("dev", false, "include development source maps")
	listen := flag.String("listen", "127.0.0.1:8080", "development server address")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, ".", *live, *dev || *live, "http://"+*listen+"/force-reload"); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, root string, live, dev bool, notify string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	directories, err := findSourceDirectories([]string{filepath.Join(root, "web/features/*"), filepath.Join(root, "web/core")})
	if err != nil {
		return err
	}
	entryPoints, err := findEntryPoints(directories)
	if err != nil {
		return err
	}
	if len(entryPoints) == 0 {
		return nil
	}

	sourceMap := api.SourceMapNone
	if dev {
		sourceMap = api.SourceMapLinked
	}
	options := api.BuildOptions{
		AbsWorkingDir: root, EntryPointsAdvanced: entryPoints, Outdir: root,
		Bundle: true, Write: false, LogLevel: api.LogLevelInfo,
		MinifyWhitespace: true, MinifyIdentifiers: true, MinifySyntax: true,
		Format: api.FormatESModule, Target: api.ESNext, Sourcemap: sourceMap,
		External: []string{"*.woff2", "datastar"},
	}
	// esbuild's in-memory output API keeps compile errors from deleting usable assets.
	options.Plugins = []api.Plugin{{
		Name: "write-success",
		Setup: func(build api.PluginBuild) {
			build.OnEnd(func(result *api.BuildResult) (api.OnEndResult, error) {
				if len(result.Errors) != 0 {
					return api.OnEndResult{}, nil
				}
				for _, output := range result.OutputFiles {
					if err := os.MkdirAll(filepath.Dir(output.Path), 0755); err != nil {
						return api.OnEndResult{}, err
					}
					if err := os.WriteFile(output.Path, output.Contents, 0644); err != nil {
						return api.OnEndResult{}, err
					}
				}
				if live {
					// The server may still be starting; its reload connection retries.
					if err := triggerReload(ctx, notify); err != nil && ctx.Err() == nil {
						log.Printf("reload notification: %v", err)
					}
				}
				return api.OnEndResult{}, nil
			})
		},
	}}
	if live {
		buildContext, err := api.Context(options)
		if err != nil {
			return err
		}
		defer buildContext.Dispose()
		if err := buildContext.Watch(api.WatchOptions{}); err != nil {
			return err
		}
		<-ctx.Done()
		return nil
	}

	result := api.Build(options)
	if len(result.Errors) > 0 {
		errs := make([]error, len(result.Errors))
		for i, buildError := range result.Errors {
			errs[i] = errors.New(buildError.Text)
		}
		return errors.Join(errs...)
	}
	if !dev {
		return removeSourceMaps(directories)
	}
	return nil
}

func findSourceDirectories(patterns []string) ([]string, error) {
	var directories []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 && !strings.Contains(pattern, "*") {
			matches = []string{pattern}
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err == nil && info.IsDir() {
				directories = append(directories, match)
			}
		}
	}
	return directories, nil
}

func findEntryPoints(directories []string) ([]api.EntryPoint, error) {
	var entryPoints []api.EntryPoint
	for _, directory := range directories {
		for _, subdirectory := range []string{"web-components", "scripts"} {
			matches, err := filepath.Glob(filepath.Join(directory, subdirectory, "*.ts"))
			if err != nil {
				return nil, err
			}
			for _, source := range matches {
				name := strings.TrimSuffix(filepath.Base(source), ".ts")
				output := filepath.Join(directory, "static", name)
				if subdirectory == "web-components" {
					output = filepath.Join(directory, "static/web-components", name)
				}
				entryPoints = append(entryPoints, api.EntryPoint{InputPath: source, OutputPath: output})
			}
		}
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "static" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".css") || strings.HasSuffix(path, ".part.css") {
				return nil
			}
			relative, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			output := filepath.Join(directory, "static/styles", strings.TrimSuffix(relative, ".css"))
			entryPoints = append(entryPoints, api.EntryPoint{InputPath: path, OutputPath: output})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return entryPoints, nil
}

func removeSourceMaps(directories []string) error {
	for _, directory := range directories {
		err := filepath.WalkDir(filepath.Join(directory, "static"), func(path string, entry os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".map") {
				return os.Remove(path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func triggerReload(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("reload notification returned %s", response.Status)
	}
	return nil
}
