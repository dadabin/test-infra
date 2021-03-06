/*
Copyright 2019 The Kubernetes Authors.

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

package crier

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	prowv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

const reporterName = "fakeReporter"

// Fake Reporter
// Sets: Which jobs should be reported
// Asserts: Which jobs are actually reported
type fakeReporter struct {
	reported         []string
	shouldReportFunc func(pj *prowv1.ProwJob) bool
	err              error
}

func (f *fakeReporter) Report(_ *logrus.Entry, pj *prowv1.ProwJob) ([]*prowv1.ProwJob, error) {
	f.reported = append(f.reported, pj.Spec.Job)
	return []*prowv1.ProwJob{pj}, f.err
}

func (f *fakeReporter) GetName() string {
	return reporterName
}

func (f *fakeReporter) ShouldReport(_ *logrus.Entry, pj *prowv1.ProwJob) bool {
	return f.shouldReportFunc(pj)
}

func TestController_Run(t *testing.T) {

	const toReconcile = "foo"
	tests := []struct {
		name         string
		job          *prowv1.ProwJob
		shouldReport bool
		reportErr    error

		expectReport  bool
		expectPatch   bool
		expectedError error
	}{
		{
			name: "reports/patches known job",
			job: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{
					Job:    "foo",
					Report: true,
				},
				Status: prowv1.ProwJobStatus{
					State: prowv1.TriggeredState,
				},
			},
			shouldReport: true,
			expectReport: true,
			expectPatch:  true,
		},
		{
			name: "doesn't report when it shouldn't",
			job: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{
					Job:    "foo",
					Report: true,
				},
				Status: prowv1.ProwJobStatus{
					State: prowv1.TriggeredState,
				},
			},
			shouldReport: false,
			expectReport: false,
		},
		{
			name:         "doesn't report nonexistant job",
			shouldReport: true,
			expectReport: false,
		},
		{
			name: "doesn't report when SkipReport=true (i.e. Spec.Report=false)",
			job: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{
					Job:    "foo",
					Report: false,
				},
			},
			shouldReport: true,
			expectReport: false,
		},
		{
			name:         "doesn't report empty job",
			job:          &prowv1.ProwJob{},
			shouldReport: true,
			expectReport: false,
		},
		{
			name: "previously-reported job isn't reported",
			job: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{
					Job:    "foo",
					Report: true,
				},
				Status: prowv1.ProwJobStatus{
					State: prowv1.TriggeredState,
					PrevReportStates: map[string]prowv1.ProwJobState{
						reporterName: prowv1.TriggeredState,
					},
				},
			},
			shouldReport: true,
			expectReport: false,
		},
		{
			name: "error is returned",
			job: &prowv1.ProwJob{
				Spec: prowv1.ProwJobSpec{
					Job:    "foo",
					Report: true,
				},
				Status: prowv1.ProwJobStatus{
					State: prowv1.TriggeredState,
				},
			},
			shouldReport:  true,
			reportErr:     errors.New("some-err"),
			expectedError: fmt.Errorf("failed to report job: %w", errors.New("some-err")),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			rp := fakeReporter{
				err: test.reportErr,
				shouldReportFunc: func(*prowv1.ProwJob) bool {
					return test.shouldReport
				},
			}

			var prowjobs []runtime.Object
			if test.job != nil {
				prowjobs = append(prowjobs, test.job)
				test.job.Name = toReconcile
			}
			cs := &patchTrackingClient{Client: fakectrlruntimeclient.NewFakeClient(prowjobs...)}
			r := &reconciler{
				ctx:         context.Background(),
				pjclientset: cs,
				reporter:    &rp,
			}

			_, err := r.Reconcile(ctrlruntime.Request{NamespacedName: types.NamespacedName{Name: toReconcile}})
			if !reflect.DeepEqual(err, test.expectedError) {
				t.Fatalf("actual err %v differs from expected err %v", err, test.expectedError)
			}
			if err != nil {
				return
			}

			var expectReports []string
			if test.expectReport {
				expectReports = []string{toReconcile}
			}
			if !reflect.DeepEqual(expectReports, rp.reported) {
				t.Errorf("mismatch report: wants %v, got %v", expectReports, rp.reported)
			}

			if (cs.patches != 0) != test.expectPatch {
				if test.expectPatch {
					t.Errorf("expected patch, but didn't get it")
				}
			}
		})
	}
}

type patchTrackingClient struct {
	ctrlruntimeclient.Client
	patches int
}

func (c *patchTrackingClient) Patch(ctx context.Context, obj runtime.Object, patch ctrlruntimeclient.Patch, opts ...ctrlruntimeclient.PatchOption) error {
	c.patches++
	return c.Client.Patch(ctx, obj, patch, opts...)
}
