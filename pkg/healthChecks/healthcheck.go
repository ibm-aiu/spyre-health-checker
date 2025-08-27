package healthchecks

import "github.com/golang/glog"

type Vitals struct {
}

func (v *Vitals) RunLSPCI() {
	glog.Info("Running lspci")
}

func (v *Vitals) GetCardStatus() {}
