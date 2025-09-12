package main

import (
	"os"

	"k8s.io/component-base/cli"
	_ "k8s.io/component-base/metrics/prometheus/clientgo" // for rest client metric registration
	_ "k8s.io/component-base/metrics/prometheus/version"  // for version metric registration
	"k8s.io/kubernetes/cmd/kube-scheduler/app"
    "sigs.k8s.io/scheduler-plugins/pkg/capacityscheduling"
	"sigs.k8s.io/scheduler-plugins/pkg/networkaware/networkoverhead"
	"sigs.k8s.io/scheduler-plugins/pkg/networkaware/topologicalsort"
	"sigs.k8s.io/scheduler-plugins/pkg/noderesources"
	"sigs.k8s.io/scheduler-plugins/pkg/noderesourcetopology"
	"sigs.k8s.io/scheduler-plugins/pkg/podstate"
	"sigs.k8s.io/scheduler-plugins/pkg/preemptiontoleration"
	"sigs.k8s.io/scheduler-plugins/pkg/qos"
	"sigs.k8s.io/scheduler-plugins/pkg/rescheduler"
	"sigs.k8s.io/scheduler-plugins/pkg/sysched"
	"sigs.k8s.io/scheduler-plugins/pkg/trimaran/loadvariationriskbalancing"
	"sigs.k8s.io/scheduler-plugins/pkg/trimaran/lowriskovercommitment"
	"sigs.k8s.io/scheduler-plugins/pkg/trimaran/peaks"
	"sigs.k8s.io/scheduler-plugins/pkg/trimaran/targetloadpacking"

	// Ensure scheme package is initialized.
	_ "sigs.k8s.io/scheduler-plugins/apis/config/scheme"
)

func main() {
	// Register custom plugins to the scheduler framework.
	// Later they can consist of scheduler profile(s) and hence
	// used by various kinds of workloads.
	command := app.NewSchedulerCommand(
		app.WithPlugin(capacityscheduling.Name, capacityscheduling.New),
		app.WithPlugin(loadvariationriskbalancing.Name, loadvariationriskbalancing.New),
		app.WithPlugin(networkoverhead.Name, networkoverhead.New),
		app.WithPlugin(topologicalsort.Name, topologicalsort.New),
		app.WithPlugin(noderesources.AllocatableName, noderesources.NewAllocatable),
		app.WithPlugin(noderesourcetopology.Name, noderesourcetopology.New),
		app.WithPlugin(preemptiontoleration.Name, preemptiontoleration.New),
		app.WithPlugin(targetloadpacking.Name, targetloadpacking.New),
		app.WithPlugin(lowriskovercommitment.Name, lowriskovercommitment.New),
		app.WithPlugin(sysched.Name, sysched.New),
		app.WithPlugin(peaks.Name, peaks.New),
		app.WithPlugin(rescheduler.Name, rescheduler.New),
		// Sample plugins below.
		// app.WithPlugin(crossnodepreemption.Name, crossnodepreemption.New),
		app.WithPlugin(podstate.Name, podstate.New),
		app.WithPlugin(qos.Name, qos.New),
	)

	code := cli.Run(command)
	os.Exit(code)
}