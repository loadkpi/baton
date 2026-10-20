package sessionfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/loadkpi/baton/business/domain/sessionbus"
	"github.com/loadkpi/baton/business/types/agent"
	"github.com/loadkpi/baton/business/types/profilename"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/runtime"
	"github.com/loadkpi/baton/foundation/atomicfile"
)

// fileLaunch is the JSON document of a launch record. Primitive types only.
type fileLaunch struct {
	V       int    `json:"v"`
	TS      string `json:"ts"`
	Agent   string `json:"agent"`
	Profile string `json:"profile"`
	Runtime string `json:"runtime"`
	Task    string `json:"task"`
	Title   string `json:"title"`
}

func toFileLaunch(l sessionbus.Launch) fileLaunch {
	return fileLaunch{
		V: eventVersion, TS: l.TS.Format(time.RFC3339Nano), Agent: l.Agent.String(),
		Profile: l.Profile.String(), Runtime: l.Runtime.String(), Task: l.Task, Title: l.Title,
	}
}

func launchDir(root string, project projectname.Name) string {
	return filepath.Join(root, "projects", project.String(), "launches")
}

// SaveLaunch writes <project dir>/launches/<agent>.json, mode 0600, replacing
// the record of the Project's previous launch of the Agent.
func (s *Store) SaveLaunch(_ context.Context, l sessionbus.Launch) error {
	data, err := json.MarshalIndent(toFileLaunch(l), "", "  ")
	if err != nil {
		return err
	}
	dir := launchDir(s.root, l.Project)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(dir, l.Agent.String()+".json"), append(data, '\n'), 0o600)
}

// QueryLaunches reads the launch records of every Project.
func (s *Store) QueryLaunches(_ context.Context) ([]sessionbus.Launch, error) {
	projects, err := os.ReadDir(filepath.Join(s.root, "projects"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var launches []sessionbus.Launch
	for _, p := range projects {
		project, err := projectname.Parse(p.Name())
		if err != nil || !p.IsDir() {
			continue
		}
		files, err := os.ReadDir(launchDir(s.root, project))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			data, err := os.ReadFile(filepath.Join(launchDir(s.root, project), f.Name()))
			if err != nil {
				continue
			}
			if l, err := toBusLaunch(project, data); err == nil {
				launches = append(launches, l)
			}
		}
	}
	return launches, nil
}

func toBusLaunch(project projectname.Name, data []byte) (sessionbus.Launch, error) {
	var fl fileLaunch
	if err := json.Unmarshal(data, &fl); err != nil {
		return sessionbus.Launch{}, err
	}
	if fl.V != eventVersion {
		return sessionbus.Launch{}, fmt.Errorf("unsupported launch version %d", fl.V)
	}
	ts, err := time.Parse(time.RFC3339Nano, fl.TS)
	if err != nil {
		return sessionbus.Launch{}, err
	}
	a, err := agent.Parse(fl.Agent)
	if err != nil {
		return sessionbus.Launch{}, err
	}
	p, err := profilename.Parse(fl.Profile)
	if err != nil {
		return sessionbus.Launch{}, err
	}
	r, err := runtime.Parse(fl.Runtime)
	if err != nil {
		return sessionbus.Launch{}, err
	}
	return sessionbus.Launch{TS: ts, Project: project, Agent: a, Profile: p, Runtime: r, Task: fl.Task, Title: fl.Title}, nil
}
