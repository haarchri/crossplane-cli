/*
Copyright 2026 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cli_test

import (
	"context"
	"io"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"

	"github.com/crossplane/cli/v2/cmd/crossplane/project"
	"github.com/crossplane/cli/v2/pkg/cli"
)

// bindingProbe takes the same bindings the embedded crossplane commands'
// Run methods take, so running it proves a host parser configured with
// Options and BindRuntime can resolve them all.
type bindingProbe struct{}

func (c *bindingProbe) Run(_ logging.Logger, _ cli.SpinnerPrinter, _ *cli.Config, _ context.Context) error {
	return nil
}

type hostCLI struct {
	Project project.Cmd  `cmd:""`
	Probe   bindingProbe `cmd:""`
}

// TestHostEmbedding verifies that a host CLI embedding the crossplane project
// commands can construct a parser with Options (kong.Vars present), parse a
// project command, and resolve every binding its Run methods require.
func TestHostEmbedding(t *testing.T) {
	cfg := &cli.Config{}
	logger := logging.NewNopLogger()

	parser, err := kong.New(&hostCLI{},
		append([]kong.Option{kong.Name("host"), kong.Exit(func(int) {})},
			cli.Options(logger, cfg)...)...)
	if err != nil {
		t.Fatalf("kong.New() with cli.Options() failed: %v", err)
	}

	// Parsing a project subcommand exercises the ${project_file} var.
	if _, err := parser.Parse([]string{"project", "stop"}); err != nil {
		t.Fatalf("parser.Parse(project stop) failed: %v", err)
	}

	// Running the probe exercises every binding type.
	k, err := parser.Parse([]string{"probe"})
	if err != nil {
		t.Fatalf("parser.Parse(probe) failed: %v", err)
	}
	cli.BindRuntime(k, cli.NewSpinnerPrinter(io.Discard, false), context.Background())
	if err := k.Run(); err != nil {
		t.Fatalf("k.Run() failed to resolve bindings: %v", err)
	}
}
