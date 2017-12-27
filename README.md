# kubesat

Terminal dashboard with various info from Kubernetes, AWS.

Uses your `~/.kube/config` to find a Kubernetes cluster.

Currently is read-only, though probably won't be forever, so use with care!

Usage (use `go install` for incremental compilation):

```
$ cd $GOPATH/src/github.com/raisemarketplace/kubesat
$ go install && kubesat
```

