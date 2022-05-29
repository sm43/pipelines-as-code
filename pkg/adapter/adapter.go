package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/version"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketserver"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab"
	"go.uber.org/zap"
	v1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"knative.dev/eventing/pkg/adapter/v2"
	"knative.dev/pkg/logging"
)

var scheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(scheme)

func init() {
	addToScheme(scheme)
}

func addToScheme(scheme *runtime.Scheme) {
	v1.AddToScheme(scheme)
}

type envConfig struct {
	adapter.EnvConfig
}

func NewEnvConfig() adapter.EnvConfigAccessor {
	return &envConfig{}
}

type listener struct {
	run    *params.Run
	kint   *kubeinteraction.Interaction
	logger *zap.SugaredLogger
}

type Response struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

var _ adapter.Adapter = (*listener)(nil)

func New(run *params.Run, k *kubeinteraction.Interaction) adapter.AdapterConstructor {
	return func(ctx context.Context, processed adapter.EnvConfigAccessor, ceClient cloudevents.Client) adapter.Adapter {
		return &listener{
			logger: logging.FromContext(ctx),
			run:    run,
			kint:   k,
		}
	}
}

func (l *listener) Start(_ context.Context) error {
	l.logger.Infof("Starting Pipelines as Code version: %s", version.Version)

	mux := http.NewServeMux()

	// for handling probes
	mux.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = fmt.Fprint(w, "ok")
	})

	mux.HandleFunc("/", l.handleEvent())

	mux.HandleFunc("/validate", admitFunc(l.validate).serve(l.run))

	srv := &http.Server{
		Addr: ":8080",
		Handler: http.TimeoutHandler(mux,
			10*time.Second, "Listener Timeout!\n"),
	}

	// TODO: support TLS/Certs
	if err := srv.ListenAndServe(); err != nil {
		return err
	}

	return nil
}

func (l listener) handleEvent() http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		ctx := context.Background()

		if request.Method != http.MethodPost {
			l.writeResponse(response, http.StatusOK, "ok")
			return
		}

		// event body
		payload, err := ioutil.ReadAll(request.Body)
		if err != nil {
			l.logger.Errorf("failed to read body : %v", err)
			response.WriteHeader(http.StatusInternalServerError)
			return
		}

		// payload validation
		var event map[string]interface{}
		if err := json.Unmarshal(payload, &event); err != nil {
			l.logger.Errorf("Invalid event body format format: %s", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}

		// figure out which provider request coming from
		gitProvider, logger, err := l.detectProvider(&request.Header, string(payload))
		if err != nil || gitProvider == nil {
			l.writeResponse(response, http.StatusOK, err.Error())
			return
		}

		s := sinker{
			run:    l.run,
			vcx:    gitProvider,
			kint:   l.kint,
			event:  info.NewEvent(),
			logger: logger,
		}

		// clone the request to use it further
		localRequest := request.Clone(request.Context())

		go func() {
			err := s.processEvent(ctx, localRequest, payload)
			if err != nil {
				logger.Errorf("an error occurred: %v", err)
			}
		}()

		l.writeResponse(response, http.StatusAccepted, "accepted")
	}
}

func (l listener) detectProvider(reqHeader *http.Header, reqBody string) (provider.Interface, *zap.SugaredLogger, error) {
	log := *l.logger

	processRes := func(processEvent bool, provider provider.Interface, logger *zap.SugaredLogger, skipReason string,
		err error,
	) (provider.Interface, *zap.SugaredLogger, error) {
		if processEvent {
			provider.SetLogger(logger)
			return provider, logger, nil
		}
		if err != nil {
			errStr := fmt.Sprintf("got error while processing : %v", err)
			logger.Error(errStr)
			return nil, logger, fmt.Errorf(errStr)
		}

		if skipReason != "" {
			logger.Infof("skipping event: %s", skipReason)
		}
		return nil, logger, fmt.Errorf("skipping event")
	}

	gitHub := &github.Provider{}
	isGH, processReq, logger, reason, err := gitHub.Detect(reqHeader, reqBody, &log)
	if isGH {
		return processRes(processReq, gitHub, logger, reason, err)
	}

	bitServer := &bitbucketserver.Provider{}
	isBitServer, processReq, logger, reason, err := bitServer.Detect(reqHeader, reqBody, &log)
	if isBitServer {
		return processRes(processReq, bitServer, logger, reason, err)
	}

	gitLab := &gitlab.Provider{}
	isGitlab, processReq, logger, reason, err := gitLab.Detect(reqHeader, reqBody, &log)
	if isGitlab {
		return processRes(processReq, gitLab, logger, reason, err)
	}

	bitCloud := &bitbucketcloud.Provider{}
	isBitCloud, processReq, logger, reason, err := bitCloud.Detect(reqHeader, reqBody, &log)
	if isBitCloud {
		return processRes(processReq, bitCloud, logger, reason, err)
	}

	return processRes(false, nil, logger, "", fmt.Errorf("no supported Git provider has been detected"))
}

func (l listener) writeResponse(response http.ResponseWriter, statusCode int, message string) {
	response.WriteHeader(statusCode)
	response.Header().Set("Content-Type", "application/json")
	body := Response{
		Status:  statusCode,
		Message: message,
	}
	if err := json.NewEncoder(response).Encode(body); err != nil {
		l.logger.Errorf("failed to write back sink response: %v", err)
	}
}

type admitFunc func(v1.AdmissionReview, *params.Run) *v1.AdmissionResponse

var (
	universalDeserializer = serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
)

func (l listener) validate(ar v1.AdmissionReview, run *params.Run) *v1.AdmissionResponse {
	reviewResponse := v1.AdmissionResponse{}
	reviewResponse.Allowed = true

	raw := ar.Request.Object.Raw
	repo := v1alpha1.Repository{}
	if _, _, err := universalDeserializer.Decode(raw, nil, &repo); err != nil {
		l.logger.Error(err)
		return toAdmissionResponse(err)
	}

	exist, err := CheckIfRepoExist(context.Background(), run, &repo, "")
	if err != nil {
		l.logger.Error(err)
		return toAdmissionResponse(err)
	}

	if exist {
		toAdmissionResponse(fmt.Errorf("repository already exist"))
	}

	return &reviewResponse
}

func serve(w http.ResponseWriter, r *http.Request, admit admitFunc, run *params.Run) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		fmt.Printf("contentType=%s, expect application/json", contentType)
		return
	}

	var reviewResponse *v1.AdmissionResponse
	ar := v1.AdmissionReview{}
	deserializer := codecs.UniversalDeserializer()
	if _, _, err := deserializer.Decode(body, nil, &ar); err != nil {
		fmt.Println(err)
		reviewResponse = toAdmissionResponse(err)
	}

	response := v1.AdmissionReview{}

	if ar.Request != nil {
		reviewResponse = admit(ar, run)
		fmt.Printf("sending response: %v", reviewResponse)

		if reviewResponse != nil {
			response.Response = reviewResponse
			response.Response.UID = ar.Request.UID
		}
		// reset the Object and OldObject, they are not needed in a response.
		ar.Request.Object = runtime.RawExtension{}
		ar.Request.OldObject = runtime.RawExtension{}
	} else {
		response.Response = toAdmissionResponse(fmt.Errorf("Invalid admission request"))
	}

	resp, err := json.Marshal(response)
	if err != nil {
		fmt.Println(err)
	}
	if _, err := w.Write(resp); err != nil {
		fmt.Println(err)
	}

}

func (fn admitFunc) serve(run *params.Run) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fn, run)
	}
}

func toAdmissionResponse(err error) *v1.AdmissionResponse {
	return &v1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func CheckIfRepoExist(ctx context.Context, cs *params.Run, repo *v1alpha1.Repository, ns string) (bool, error) {
	repositories, err := cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns).List(
		ctx, metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	for i := len(repositories.Items) - 1; i >= 0; i-- {
		repoFromCluster := repositories.Items[i]
		if repoFromCluster.Spec.URL == repo.Spec.URL {
			return true, nil
		}
	}

	return false, nil
}
