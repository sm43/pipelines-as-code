package info

type Info struct {
	pac        *PacOpts
	Kube       *KubeOpts
	Controller *ControllerInfo
}

func (i *Info) GetPac() PacOpts {
	return *i.pac
}

func (i *Info) DeepCopy(out *Info) {
	*out = *i
}

type (
	contextKey string
)
