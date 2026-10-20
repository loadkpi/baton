// Package taskfile is the file storage of the Task domain: a record per Task
// in <project>/tasks and the journal <project>/current.jsonl.
package taskfile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/loadkpi/baton/business/domain/taskbus"
	"github.com/loadkpi/baton/business/types/projectname"
	"github.com/loadkpi/baton/business/types/taskid"
	"github.com/loadkpi/baton/business/types/taskname"
	"github.com/loadkpi/baton/business/types/taskstatus"
	"github.com/loadkpi/baton/foundation/atomicfile"
	"github.com/loadkpi/baton/foundation/filelock"
	"github.com/loadkpi/baton/foundation/jsonl"
)

// switchVersion is the schema version of a journal line.
const switchVersion = 1

// Store keeps the records of every Project under <baton dir>/projects.
type Store struct {
	dir string
	// mounted, when set, is the directory of the one Project this Store
	// knows, wherever it is: the Project name is not used to find it.
	mounted string
}

// New creates the storage. batonDir is the Baton state directory (~/.baton).
func New(batonDir string) *Store {
	return &Store{dir: filepath.Join(batonDir, "projects")}
}

// NewMounted creates the storage inside a container, where the Project's Baton
// directory is mounted at projectDir. The name of the Project is not known
// there and not used: every Project name stands for that directory.
func NewMounted(projectDir string) *Store {
	return &Store{mounted: projectDir}
}

func (s *Store) projectDir(p projectname.Name) string {
	if s.mounted != "" {
		return s.mounted
	}
	return filepath.Join(s.dir, p.String())
}

// fileTask is the document of tasks/<id>.toml. Primitive types only. The id is
// the name of the file.
type fileTask struct {
	Name    string `toml:"name,omitempty"`
	Status  string `toml:"status"`
	Created string `toml:"created"`
}

func toFileTask(t taskbus.Task) fileTask {
	return fileTask{Name: t.Name.String(), Status: t.Status.String(), Created: t.Created.UTC().Format(time.RFC3339Nano)}
}

func toBusTask(id string, ft fileTask) (taskbus.Task, error) {
	tid, err := taskid.Parse(id)
	if err != nil {
		return taskbus.Task{}, err
	}
	var name taskname.Name
	if ft.Name != "" {
		if name, err = taskname.Parse(ft.Name); err != nil {
			return taskbus.Task{}, err
		}
	}
	st, err := taskstatus.Parse(ft.Status)
	if err != nil {
		return taskbus.Task{}, err
	}
	created, err := time.Parse(time.RFC3339Nano, ft.Created)
	if err != nil {
		return taskbus.Task{}, err
	}
	return taskbus.Task{ID: tid, Name: name, Status: st, Created: created}, nil
}

// fileSwitch is the JSON document of one journal line. Primitive types only.
type fileSwitch struct {
	V           int    `json:"v"`
	TS          string `json:"ts"`
	WorkingCopy string `json:"working_copy"`
	Task        string `json:"task"`
}

func toFileSwitch(sw taskbus.Switch) fileSwitch {
	return fileSwitch{
		V: switchVersion, TS: sw.At.UTC().Format(time.RFC3339Nano),
		WorkingCopy: sw.WorkingCopy, Task: sw.Task.String(),
	}
}

func toBusSwitch(line []byte) (taskbus.Switch, error) {
	var fs fileSwitch
	if err := json.Unmarshal(line, &fs); err != nil {
		return taskbus.Switch{}, err
	}
	if fs.V != switchVersion {
		return taskbus.Switch{}, fmt.Errorf("unsupported journal version %d", fs.V)
	}
	at, err := time.Parse(time.RFC3339Nano, fs.TS)
	if err != nil {
		return taskbus.Switch{}, err
	}
	id, err := taskid.Parse(fs.Task)
	if err != nil {
		return taskbus.Switch{}, err
	}
	return taskbus.Switch{At: at, WorkingCopy: fs.WorkingCopy, Task: id}, nil
}

// QueryTask reads tasks/<id>.toml.
func (s *Store) QueryTask(_ context.Context, project projectname.Name, id taskid.ID) (taskbus.Task, bool, error) {
	file := filepath.Join(s.projectDir(project), "tasks", id.String()+".toml")
	var ft fileTask
	if _, err := toml.DecodeFile(file, &ft); err != nil {
		if os.IsNotExist(err) {
			return taskbus.Task{}, false, nil
		}
		return taskbus.Task{}, false, fmt.Errorf("%s: %w", file, err)
	}
	t, err := toBusTask(id.String(), ft)
	if err != nil {
		return taskbus.Task{}, false, fmt.Errorf("%s: %w", file, err)
	}
	return t, true, nil
}

// QueryTasks reads every tasks/*.toml of the Project.
func (s *Store) QueryTasks(ctx context.Context, project projectname.Name) ([]taskbus.Task, error) {
	dir := filepath.Join(s.projectDir(project), "tasks")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tasks []taskbus.Task
	for _, e := range entries {
		idText, isRecord := strings.CutSuffix(e.Name(), ".toml")
		if !isRecord || !e.Type().IsRegular() {
			continue
		}
		id, err := taskid.Parse(idText)
		if err != nil {
			continue // not a record of ours
		}
		t, _, err := s.QueryTask(ctx, project, id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// SaveTask writes tasks/<id>.toml atomically.
func (s *Store) SaveTask(_ context.Context, project projectname.Name, t taskbus.Task) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(toFileTask(t)); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(s.projectDir(project), "tasks", t.ID.String()+".toml"), buf.Bytes(), 0o644)
}

// QuerySwitches reads current.jsonl, skipping lines that are not journal
// lines.
func (s *Store) QuerySwitches(_ context.Context, project projectname.Name) ([]taskbus.Switch, error) {
	var switches []taskbus.Switch
	err := jsonl.Forward(filepath.Join(s.projectDir(project), "current.jsonl"), func(line []byte) bool {
		if sw, err := toBusSwitch(line); err == nil {
			switches = append(switches, sw)
		}
		return true
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return switches, err
}

// SaveSwitch appends a line to current.jsonl.
func (s *Store) SaveSwitch(_ context.Context, project projectname.Name, sw taskbus.Switch) error {
	line, err := json.Marshal(toFileSwitch(sw))
	if err != nil {
		return err
	}
	return jsonl.Append(filepath.Join(s.projectDir(project), "current.jsonl"), line, 0o644)
}

// Lock takes <project>/.lock.
func (s *Store) Lock(_ context.Context, project projectname.Name) (func(), error) {
	return filelock.Acquire(filepath.Join(s.projectDir(project), ".lock"))
}
