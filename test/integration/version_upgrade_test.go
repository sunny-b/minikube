/*
Copyright 2020 The Kubernetes Authors All rights reserved.

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

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/docker/machine/libmachine/state"

	"k8s.io/minikube/pkg/minikube/constants"
	"k8s.io/minikube/pkg/util/retry"

	"github.com/hashicorp/go-getter"
	pkgutil "k8s.io/minikube/pkg/util"
)

// TestVersionUpgrade downloads the latest version of minikube and runs with
// the oldest supported k8s version and then runs the current head minikube
// and tries to upgrade from the oldest supported k8s to newest supported k8s
func TestVersionUpgrade(t *testing.T) {
	MaybeParallel(t)
	profile := UniqueProfileName("vupgrade")
	ctx, cancel := context.WithTimeout(context.Background(), Minutes(55))

	defer CleanupWithLogs(t, profile, cancel)

	tf, err := ioutil.TempFile("", "minikube-release.*.exe")
	if err != nil {
		t.Fatalf("tempfile: %v", err)
	}
	defer os.Remove(tf.Name())
	tf.Close()

	url := pkgutil.GetBinaryDownloadURL("latest", runtime.GOOS)
	if err := retry.Expo(func() error { return getter.GetFile(tf.Name(), url) }, 3*time.Second, Minutes(3)); err != nil {
		t.Fatalf("get failed: %v", err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(tf.Name(), 0700); err != nil {
			t.Errorf("chmod: %v", err)
		}
	}

	// Assert that --iso-url works without a sha checksum, and that we can upgrade from old ISO's
	// Some day, this will break an implicit assumption that a tool is available in the ISO :)
	oldISO := "https://storage.googleapis.com/minikube/iso/integration-test.iso"
	args := append([]string{"start", "-p", profile, "--memory=2200", fmt.Sprintf("--iso-url=%s", oldISO), fmt.Sprintf("--kubernetes-version=%s", constants.OldestKubernetesVersion), "--alsologtostderr"}, StartArgs()...)
	rr := &RunResult{}
	r := func() error {
		rr, err = Run(t, exec.CommandContext(ctx, tf.Name(), args...))
		return err
	}

	// Retry up to two times, to allow flakiness for the previous release
	if err := retry.Expo(r, 1*time.Second, Minutes(30), 2); err != nil {
		t.Fatalf("release start failed: %v", err)
	}

	rr, err = Run(t, exec.CommandContext(ctx, tf.Name(), "stop", "-p", profile))
	if err != nil {
		t.Fatalf("%s failed: %v", rr.Command(), err)
	}

	rr, err = Run(t, exec.CommandContext(ctx, tf.Name(), "-p", profile, "status", "--format={{.Host}}"))
	if err != nil {
		t.Logf("status error: %v (may be ok)", err)
	}

	got := strings.TrimSpace(rr.Stdout.String())
	if got != state.Stopped.String() {
		t.Errorf("FAILED: status = %q; want = %q", got, state.Stopped.String())
	}

	args = append([]string{"start", "-p", profile, "--memory=2200", fmt.Sprintf("--kubernetes-version=%s", constants.NewestKubernetesVersion), "--alsologtostderr", "-v=1"}, StartArgs()...)
	rr, err = Run(t, exec.CommandContext(ctx, Target(), args...))
	if err != nil {
		t.Errorf("failed to start minikube HEAD with newest k8s version. args: %s : %v", rr.Command(), err)
	}

	s, err := Run(t, exec.CommandContext(ctx, "kubectl", "--context", profile, "version", "--output=json"))
	if err != nil {
		t.Fatalf("error running kubectl: %v", err)
	}
	cv := struct {
		ServerVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"serverVersion"`
	}{}
	err = json.Unmarshal(s.Stdout.Bytes(), &cv)

	if err != nil {
		t.Fatalf("error traversing json output: %v", err)
	}

	if cv.ServerVersion.GitVersion != constants.NewestKubernetesVersion {
		t.Fatalf("expected server version %s is not the same with latest version %s", cv.ServerVersion.GitVersion, constants.NewestKubernetesVersion)
	}

	t.Logf("Attempting to downgrade Kubernetes (should fail)")
	args = append([]string{"start", "-p", profile, "--memory=2200", fmt.Sprintf("--kubernetes-version=%s", constants.OldestKubernetesVersion)}, StartArgs()...)
	if rr, err := Run(t, exec.CommandContext(ctx, tf.Name(), args...)); err == nil {
		t.Fatalf("downgrading Kubernetes should not be allowed. expected to see error but got %v for %q", err, rr.Command())
	}

	t.Logf("Attempting restart after unsuccessful downgrade")
	args = append([]string{"start", "-p", profile, "--memory=2200", fmt.Sprintf("--kubernetes-version=%s", constants.NewestKubernetesVersion), "--alsologtostderr", "-v=1"}, StartArgs()...)
	rr, err = Run(t, exec.CommandContext(ctx, Target(), args...))
	if err != nil {
		t.Errorf("start after failed upgrade: %v", err)
	}
}
