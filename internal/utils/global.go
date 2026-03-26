package utils

import (
	"os"
)

var NodeName string = os.Getenv("NODE_NAME")
var Namespace string = os.Getenv("NAMESPACE")
var PodName string = os.Getenv("POD_NAME")
