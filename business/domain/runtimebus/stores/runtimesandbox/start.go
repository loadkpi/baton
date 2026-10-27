package runtimesandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/loadkpi/baton/business/domain/projectbus"
	"github.com/loadkpi/baton/business/domain/runtimebus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/projectname"
)

// apiKeyVars are cleared inside the container before the Agent starts (spec
// story 30). run.sh forwards no variable of the host by name, so they could
// only come from the credentials file the user gave run.sh or from a changed
// run.sh; `env -u` in front of the Agent covers every such source.
var apiKeyVars = []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}

// Start prepares the launch of an Agent in the Project's sandbox and describes
// the tab that runs it. ai-sandbox is used as it is, through its print-args
// mode (ADR 0001):
//
//  1. run.sh of the Project renders its configuration and prints the `docker
//     run` command, one word per line, instead of running it;
//  2. Baton's fragments are merged into the rendered Codex config and
//     instructions;
//  3. the image is built, as run.sh would, when it does not exist;
//  4. the two Baton mounts are added to the printed command, and the mounts
//     are recorded for HostPath.
//
// The tab then runs the printed command with the mounts added.
func (r *Runtime) Start(ctx context.Context, req runtimebus.StartRequest) (runtimebus.Tab, error) {
	project, err := r.project(ctx, req.Project)
	if err != nil {
		return runtimebus.Tab{}, err
	}
	if info, err := os.Stat(filepath.Join(project.Path, ".ai-sandbox", "run.sh")); err != nil || !info.Mode().IsRegular() {
		return runtimebus.Tab{}, fmt.Errorf("project %s has no .ai-sandbox/run.sh", project.Name)
	}

	binDir := filepath.Join(req.Home, ".baton", "bin")
	if info, err := os.Stat(filepath.Join(binDir, "baton")); err != nil || !info.Mode().IsRegular() {
		return runtimebus.Tab{}, fmt.Errorf("%s does not exist; run make install, the status hooks in the sandbox call it", filepath.Join(binDir, "baton"))
	}
	// Docker would make a missing mount source itself, as root.
	projectDir := filepath.Join(req.Home, ".baton", "projects", project.Name.String())
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		return runtimebus.Tab{}, err
	}

	command := agentCommand(req.Agent)
	printed, err := r.printArgs(ctx, project, command)
	if err != nil {
		return runtimebus.Tab{}, err
	}
	if err := r.ensureImage(ctx, project, printed.image); err != nil {
		return runtimebus.Tab{}, err
	}
	if err := mergeFragments(filepath.Join(homeOf(project), ".codex")); err != nil {
		return runtimebus.Tab{}, err
	}

	// The Baton mounts, and nothing else of ~/.baton.
	batonMounts := []mount{
		{Host: binDir, Container: ContainerBinDir, ReadOnly: true},
		{Host: projectDir, Container: ContainerProject},
	}
	words := append([]string{"docker"}, printed.options...)
	for _, m := range batonMounts {
		words = append(words, "-v", m.spec())
	}
	words = append(words, printed.image)
	words = append(words, command...)
	if req.Prompt != "" {
		// After the command run.sh printed: the prompt is for the Agent, and
		// run.sh has no use for it.
		words = append(words, req.Prompt)
	}

	if err := r.saveMounts(project.Name, append(mountsOf(printed.options), batonMounts...)); err != nil {
		return runtimebus.Tab{}, err
	}

	title := req.Project.String() + "/" + req.Task + ":" + req.Agent.String()
	return runtimebus.Tab{Dir: project.Path, Title: title, Command: words, Expect: []string{"docker"}}, nil
}

// project finds a registered Project by its name.
func (r *Runtime) project(ctx context.Context, name projectname.Name) (projectbus.Project, error) {
	all, err := r.projects.All(ctx)
	if err != nil {
		return projectbus.Project{}, err
	}
	for _, p := range all {
		if p.Name == name {
			return p, nil
		}
	}
	return projectbus.Project{}, fmt.Errorf("unknown project %q", name)
}

// agentCommand is what runs inside the container: the Agent, with the
// variables that would switch it to API billing removed.
func agentCommand(a agent.Agent) []string {
	command := []string{"env"}
	for _, name := range apiKeyVars {
		command = append(command, "-u", name)
	}
	return append(command, a.Binary())
}

// printed is the `docker run` command run.sh printed, taken apart.
type printed struct {
	// options are the words between `docker` and the image, `run` first.
	options []string
	image   string
}

// printArgs runs the Project's own run.sh in print-args mode, once, with the
// Agent's command, and takes the printed command apart: `docker`, the options,
// the image and then the command, one word per line (DESIGN.md, D3; the output
// format is a contract with ai-sandbox, ADR 0001).
func (r *Runtime) printArgs(ctx context.Context, p projectbus.Project, command []string) (printed, error) {
	script := filepath.Join(p.Path, ".ai-sandbox", "run.sh")
	cmd := exec.CommandContext(ctx, "bash", append([]string{script, "--"}, command...)...)
	cmd.Dir = p.Path
	cmd.Env = append(os.Environ(), "AI_SANDBOX_PRINT_ARGS=1")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = r.log
	if err := cmd.Run(); err != nil {
		return printed{}, fmt.Errorf("%s: %w", script, err)
	}

	words := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	imageAt := len(words) - len(command) - 1
	if imageAt < 2 || words[0] != "docker" || words[1] != "run" || strings.Join(words[imageAt+1:], "\n") != strings.Join(command, "\n") {
		return printed{}, errors.New("run.sh printed something other than `docker run ... <image> " + strings.Join(command, " ") + "`; Baton relies on its print-args output")
	}
	return printed{options: words[1:imageAt], image: words[imageAt]}, nil
}

// ensureImage builds the image with the Project's build.sh when it does not
// exist, as run.sh does before it runs docker (print-args mode skips that).
func (r *Runtime) ensureImage(ctx context.Context, p projectbus.Project, image string) error {
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", image).Run(); err == nil {
		return nil
	}
	build := filepath.Join(p.Path, ".ai-sandbox", "build.sh")
	if info, err := os.Stat(build); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("image %s does not exist and project %s has no .ai-sandbox/build.sh to build it", image, p.Name)
	}
	_, _ = fmt.Fprintf(r.log, "baton: image %s does not exist; building it with .ai-sandbox/build.sh\n", image)
	cmd := exec.CommandContext(ctx, "bash", build)
	cmd.Dir = p.Path
	cmd.Stdout, cmd.Stderr = r.log, r.log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build image %s: %w", image, err)
	}
	return nil
}
