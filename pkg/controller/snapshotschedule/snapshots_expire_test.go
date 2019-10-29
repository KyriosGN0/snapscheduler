// nolint funlen  // Long test functions ok
package snapshotschedule

import (
	"context"
	"strings"
	"testing"
	"time"

	snapschedulerv1alpha1 "github.com/backube/snapscheduler/pkg/apis/snapscheduler/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	tlogr "github.com/go-logr/logr/testing"
	snapv1alpha1 "github.com/kubernetes-csi/external-snapshotter/pkg/apis/volumesnapshot/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var nullLogger = tlogr.NullLogger{}

func fakeClient(initialObjects []runtime.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = snapschedulerv1alpha1.SchemeBuilder.AddToScheme(scheme)
	_ = snapv1alpha1.AddToScheme(scheme)
	return fake.NewFakeClientWithScheme(scheme, initialObjects...)
}

func TestGetExpirationTime(t *testing.T) {
	s := &snapschedulerv1alpha1.SnapshotSchedule{}

	// No retention time set
	expiration, err := getExpirationTime(s, time.Now(), nullLogger)
	if expiration != nil || err != nil {
		t.Errorf("empty spec.retention.expires. expected: nil,nil -- got: %v,%v", expiration, err)
	}

	// Unparsable retention time
	s.Spec.Retention.Expires = "garbage"
	_, err = getExpirationTime(s, time.Now(), nullLogger)
	if err == nil {
		t.Errorf("invalid spec.retention.expires. expected: error -- got: nil")
	}

	// Negative retention time
	s.Spec.Retention.Expires = "-10s"
	_, err = getExpirationTime(s, time.Now(), nullLogger)
	if err == nil {
		t.Errorf("negative spec.retention.expires. expected: error -- got: nil")
	}

	s.Spec.Retention.Expires = "1h"
	theTime, _ := time.Parse(timeFormat, "2013-02-01T11:04:05Z")
	expected := theTime.Add(-1 * time.Hour)
	expiration, err = getExpirationTime(s, theTime, nullLogger)
	if err != nil {
		t.Errorf("unexpected error return. expected: nil -- got: %v", err)
	}
	if expiration == nil || expected != *expiration {
		t.Errorf("incorrect expiration time. expected: %v -- got: %v", expected, expiration)
	}
}

func TestFilterExpiredSnaps(t *testing.T) {
	threshold, _ := time.Parse(timeFormat, "2000-01-01T00:00:00Z")
	times := []string{
		"1990-01-01T00:00:00Z", // expired
		"2010-02-10T10:30:05Z",
		"1999-12-31T23:59:00Z", // expired
		"2001-01-01T00:00:00Z",
		"2005-01-01T00:00:00Z",
	}
	expired := 2

	inList := &snapv1alpha1.VolumeSnapshotList{}
	for _, i := range times {
		theTime, _ := time.Parse(timeFormat, i)
		inList.Items = append(inList.Items, snapv1alpha1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.Time{
					Time: theTime,
				},
			},
		})
	}

	outList := filterExpiredSnaps(inList, threshold)
	if outList == nil {
		t.Error("unexpected nil output")
	}
	if len(outList.Items) != expired {
		t.Errorf("incorrect snapshots filtered. expected: %v -- got: %v", expired, len(outList.Items))
	}
}

func TestSnapshotsFromSchedule(t *testing.T) {
	objects := []runtime.Object{
		&snapv1alpha1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "foo",
				Namespace: "default",
				Labels: map[string]string{
					"foo":       "bar",
					ScheduleKey: "s1",
				},
			},
		},
		&snapv1alpha1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bar",
				Namespace: "default",
				Labels: map[string]string{
					"foo":       "bar",
					ScheduleKey: "s1",
				},
			},
		},
		&snapv1alpha1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "baz",
				Namespace: "default",
				Labels: map[string]string{
					"foo":       "bar",
					ScheduleKey: "s2",
				},
			},
		},
	}
	c := fakeClient(objects)
	s := &snapschedulerv1alpha1.SnapshotSchedule{}

	s.Name = "%%!! Invalid !!%%"
	_, err := snapshotsFromSchedule(s, nullLogger, c)
	if err == nil {
		t.Errorf("invalid schedule name should have produced an error")
	}

	s.Name = "s1"
	snapList, err := snapshotsFromSchedule(s, nullLogger, c)
	if err != nil {
		t.Errorf("unexpected error. got: %v", err)
	}
	if len(snapList.Items) != 2 {
		t.Errorf("matched wrong number of snapshots. expected: 2 -- got: %v", len(snapList.Items))
	}
	for _, snap := range snapList.Items {
		if snap.Name != "foo" && snap.Name != "bar" {
			t.Errorf("matched wrong snapshots. found: %v", snap.Name)
		}
	}
}

func TestExpireByTime(t *testing.T) {
	s := &snapschedulerv1alpha1.SnapshotSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "schedule",
			Namespace: "same",
		},
	}
	s.Spec.Retention.Expires = "24h"

	noexpire := &snapschedulerv1alpha1.SnapshotSchedule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "schedule",
			Namespace: "same",
		},
	}

	now := time.Now()

	data := []struct {
		namespace   string
		created     time.Time
		schedule    string
		wantExpired bool
	}{
		{"same", now.Add(-1 * time.Hour), "schedule", false},
		{"different", now.Add(-1 * time.Hour), "schedule", false},
		{"same", now.Add(-48 * time.Hour), "schedule", true},
		{"different", now.Add(-48 * time.Hour), "schedule", false},
		{"same", now.Add(-1 * time.Hour), "different", false},
		{"different", now.Add(-1 * time.Hour), "different", false},
		{"same", now.Add(-48 * time.Hour), "different", false},
		{"different", now.Add(-48 * time.Hour), "different", false},
	}
	var objects []runtime.Object
	for _, d := range data {
		objects = append(objects, &snapv1alpha1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:              d.namespace + "-" + d.schedule + "-" + d.created.Format("200601021504"),
				Namespace:         d.namespace,
				CreationTimestamp: metav1.Time{Time: d.created},
				Labels: map[string]string{
					"foo":       "bar",
					ScheduleKey: d.schedule,
				},
			},
		})
	}

	c := fakeClient(objects)

	err := expireByTime(noexpire, nullLogger, c)
	if err != nil {
		t.Errorf("unexpected error. got: %v", err)
	}
	snapList := &snapv1alpha1.VolumeSnapshotList{}
	_ = c.List(context.TODO(), &client.ListOptions{}, snapList)
	if len(snapList.Items) != len(data) {
		t.Errorf("wrong number of snapshots remain. expected: %v -- got: %v", len(data), len(snapList.Items))
	}

	err = expireByTime(s, nullLogger, c)
	if err != nil {
		t.Errorf("unexpected error. got: %v", err)
	}
	snapList = &snapv1alpha1.VolumeSnapshotList{}
	_ = c.List(context.TODO(), &client.ListOptions{}, snapList)
	if len(snapList.Items) != len(data)-1 {
		t.Errorf("wrong number of snapshots remain. expected: %v -- got: %v", len(data)-1, len(snapList.Items))
	}
}

func TestGroupSnapsByPVC(t *testing.T) {
	data := []struct {
		snapName string
		pvcName  string
	}{
		// testdata: s/^pvc/snap/ to get start of snap name
		{"snap1-1", "pvc1"},
		{"snap2-1", "pvc2"},
		{"snap1-2", "pvc1"},
		{"snap2-2", "pvc2"},
		{"snap3-blah", "pvc3"},
	}
	snapList := &snapv1alpha1.VolumeSnapshotList{}
	for _, d := range data {
		snapList.Items = append(snapList.Items, snapv1alpha1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name: d.snapName,
			},
			Spec: snapv1alpha1.VolumeSnapshotSpec{
				Source: &v1.TypedLocalObjectReference{
					Name: d.pvcName,
				},
			},
		})
	}

	groupedSnaps := groupSnapsByPVC(snapList)
	wantSnaps := len(data)
	foundSnaps := 0
	for pvcName, list := range groupedSnaps {
		wantPrefix := strings.Replace(pvcName, "pvc", "snap", -1)
		for _, snap := range list.Items {
			foundSnaps++
			if !strings.HasPrefix(snap.Name, wantPrefix) {
				t.Errorf("Improper snapshot grouping. PVC name: %v -- snap name: %v", pvcName, snap.Name)
			}
		}
	}
	if wantSnaps != foundSnaps {
		t.Errorf("Total number of grouped snaps is wrong. expected: %v -- got: %v", wantSnaps, foundSnaps)
	}
}

func TestSortSnapsByTime(t *testing.T) {
	now := time.Now()
	inSnapList := &snapv1alpha1.VolumeSnapshotList{
		Items: []snapv1alpha1.VolumeSnapshot{
			snapv1alpha1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: now.Add(1 * time.Hour)},
				},
			},
			snapv1alpha1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: now.Add(-1 * time.Hour)},
				},
			},
			snapv1alpha1.VolumeSnapshot{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: metav1.Time{Time: now},
				},
			},
		},
	}
	outSnapList := sortSnapsByTime(inSnapList)
	if len(outSnapList.Items) != len(inSnapList.Items) {
		t.Errorf("wrong number of snaps. expected: %v -- got: %v", len(inSnapList.Items), len(outSnapList.Items))
	}
	if outSnapList.Items[0].CreationTimestamp.After(outSnapList.Items[1].CreationTimestamp.Time) ||
		outSnapList.Items[1].CreationTimestamp.After(outSnapList.Items[2].CreationTimestamp.Time) {
		t.Error("snapshots were not properly sorted.")
	}

	if sortSnapsByTime(nil) != nil {
		t.Error("expected nil")
	}
}
