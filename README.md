# kubesat

Terminal dashboard with various info from Kubernetes and AWS.

Primary use case, for Kubernetes clusters running on AWS, is to assist
with in-place cluster upgrades between patch versions or minor versions.

The screenshot shows a cluster in the process of being upgraded from
Kubernetes 1.6 to 1.7.

![screenshot](img/screenshot1.png)

Uses your `~/.kube/config` to find a Kubernetes cluster.

Currently is read-only, though probably won't be forever, so use with care!

## Install

```
$ go get github.com/raisemarketplace/kubesat
$ kubesat
```

## Usage

```
  --kubeconfig=FILE
    path to the kube config file (default "~/.kube/config")
  --context=NAME
    context within the kubeconfig to use
```
