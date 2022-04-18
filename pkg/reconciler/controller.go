package reconciler

import (
	"context"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	pipelineruninformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1beta1/pipelinerun"
	pipelinerunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1beta1/pipelinerun"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
)

func NewController(run *params.Run, kinteract *kubeinteraction.Interaction) func(context.Context, configmap.Watcher) *controller.Impl {
	return func(ctx context.Context, cm configmap.Watcher) *controller.Impl {
		pipelineRunInformer := pipelineruninformer.Get(ctx)

		c := &Reconciler{
			run:         run,
			kinteract:   kinteract,
			pipelinerun: pipelineRunInformer,
		}
		impl := pipelinerunreconciler.NewImpl(ctx, c)

		pipelineRunInformer.Informer().AddEventHandler(controller.HandleAll(impl.Enqueue))

		//pipelineRunInformer.Informer().AddEventHandler(controller.HandleAll(checkAndEnqueue(impl)))

		return impl
	}
}

//func checkAndEnqueue(impl *controller.Impl) func(obj interface{}) {
//	return func(obj interface{}) {
//		object, err := kmeta.DeletionHandlingAccessor(obj)
//		if err == nil {
//			impl.EnqueueKey(types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()})
//		}
//	}
//}
