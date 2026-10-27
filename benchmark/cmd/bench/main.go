// Command bench runs the Huntsman concurrency benchmark against Firecrawl, Crawl4AI and Spider.
//
//	go run ./benchmark/cmd/bench <command> [flags]
//
// Typical session:
//
//	bench provision                                  # create the VMs, write benchmark/.env
//	bench run --machine all --engine firecrawl       # run, stream output, save results
//	bench report --inject benchmark/results/index.html
//	bench destroy                                    # delete the VMs
package main

import (
	"fmt"
	"os"
)

var commands = []struct {
	name, about string
	run         func([]string)
}{
	{"provision", "create the benchmark VMs on AWS and write their addresses to benchmark/.env", cmdProvision},
	{"run", "run the concurrency ramp on machines from benchmark/.env, streaming output and saving results", cmdRun},
	{"status", "show what each machine is doing", cmdStatus},
	{"attach", "stream the output of ramps already running on the machines", cmdAttach},
	{"stop", "stop a running ramp", cmdStop},
	{"fetch", "copy results from the machines", cmdFetch},
	{"ssh", "open a shell on a machine", cmdSSH},
	{"report", "print the results table, or refresh the HTML report with --inject", cmdReport},
	{"destroy", "delete the VMs and benchmark/.env", cmdDestroy},
	{"ramp", "run the concurrency ramp on this machine with local Docker", cmdRamp},
	{"host", "print this machine's specs as JSON", cmdHost},
}

func usage() {
	fmt.Fprint(os.Stderr, "usage: go run ./benchmark/cmd/bench <command> [flags]\n\ncommands:\n")
	for _, c := range commands {
		fmt.Fprintf(os.Stderr, "  %-10s %s\n", c.name, c.about)
	}
	fmt.Fprint(os.Stderr, "\nRun `bench <command> -h` for its flags. See benchmark/README.md.\n")
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help" {
		usage()
		os.Exit(2)
	}
	for _, c := range commands {
		if c.name == os.Args[1] {
			c.run(os.Args[2:])
			return
		}
	}
	usage()
	fatalf("unknown command %q", os.Args[1])
}
