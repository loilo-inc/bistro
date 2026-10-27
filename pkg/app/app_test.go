package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/loilo-inc/bistro/pkg/dpkg"
	"github.com/loilo-inc/bistro/pkg/ldd"
)

type stubExecer struct {
	output func(string, ...string) ([]byte, error)
}

func (e stubExecer) Output(name string, args ...string) ([]byte, error) {
	return e.output(name, args...)
}

func TestCommandDispatch(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "dpkg collect",
			args: []string{"dpkg", "collect", "-o", "/unused", "libssl-dev", "libyaml-dev"},
			want: []string{"dpkg-query", "-W", "-f=${Package}\t${Version}\t${Architecture}\n", "libssl-dev", "libyaml-dev"},
		},
		{
			name: "ldd collect",
			args: []string{"ldd", "collect", "--output", "/unused", "/usr/local/bin/ruby"},
			want: []string{"ldd", "/usr/local/bin/ruby"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stop := errors.New("backend error")
			var got []string
			execer := stubExecer{output: func(name string, args ...string) ([]byte, error) {
				got = append([]string{name}, args...)
				return nil, stop
			}}
			c := (&app{dpkg: dpkg.CreateDpkg(execer), ldd: ldd.CreateLdd(execer)}).command()
			if err := c.Run(context.Background(), append([]string{"bistro"}, tt.args...)); !errors.Is(err, stop) {
				t.Fatalf("expected backend error, got %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("backend command = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecordForwardsCommandArguments(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	root := filepath.Join(dir, "rootfs")
	calls := 0
	execer := stubExecer{output: func(name string, args ...string) ([]byte, error) {
		calls++
		return nil, nil
	}}
	c := (&app{dpkg: dpkg.CreateDpkg(execer)}).command()
	err := c.Run(context.Background(), []string{
		"bistro", "dpkg", "record", "-o", root, "--",
		"sh", "-c", `printf '%s\n' "$@" > "$0"`, argsFile,
		"install", "libssl-dev", "-y", "--output", "child output",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "install\nlibssl-dev\n-y\n--output\nchild output\n"; string(got) != want {
		t.Fatalf("child arguments = %q, want %q", got, want)
	}
	if calls != 2 {
		t.Fatalf("package snapshots = %d, want 2", calls)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("rootfs not created: %v", err)
	}
}

func TestCommandRejectsMissingArguments(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"dpkg", "record", "-o", "/unused", "--"}, "requires a command"},
		{[]string{"dpkg", "collect", "-o", "/unused"}, "requires at least one package"},
		{[]string{"ldd", "collect", "-o", "/unused"}, "requires exactly one executable"},
		{[]string{"ldd", "collect", "-o", "/unused", "/one", "/two"}, "requires exactly one executable"},
		{[]string{"dpkg", "record", "--", "echo"}, "output"},
		{[]string{"dpkg", "collect", "bash"}, "output"},
		{[]string{"ldd", "collect", "/usr/bin/perl"}, "output"},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			c := (&app{}).command()
			c.Writer = io.Discard
			c.ErrWriter = io.Discard
			err := c.Run(context.Background(), append([]string{"bistro"}, tt.args...))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}
