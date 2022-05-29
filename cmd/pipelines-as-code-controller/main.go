package main

import (
	"log"
	"os"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/adapter"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	evadapter "knative.dev/eventing/pkg/adapter/v2"
	"knative.dev/pkg/signals"
	"knative.dev/pkg/webhook"
)

const (
	PACControllerLogKey = "pipelinesascode"
)

func main() {
	ctx := signals.NewContext()

	run := params.New()
	err := run.Clients.NewClients(ctx, &run.Info)
	if err != nil {
		log.Fatal("failed to init clients : ", err)
	}

	kinteract, err := kubeinteraction.NewKubernetesInteraction(run)
	if err != nil {
		log.Fatal("failed to init kinit client : ", err)
	}

	if err := run.GetConfigFromConfigMap(ctx); err != nil {
		log.Fatal("failed to get defaults : ", err)
	}

	run.Info.Pac.LogURL = run.Clients.ConsoleUI.URL()

	secretName := os.Getenv("WEBHOOK_SECRET_NAME")
	ctx = webhook.WithOptions(ctx, webhook.Options{
		SecretName: secretName,
	})
	evadapter.MainWithContext(ctx, PACControllerLogKey, adapter.NewEnvConfig, adapter.New(run, kinteract))
}
