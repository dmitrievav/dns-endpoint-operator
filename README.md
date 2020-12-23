# dns-endpoint-operator Kubernetes operator

## Overview

`dns-endpoint-operator` is a service that helps you automatically create and update endpoints, assosiated with external kubernetes services, by periodically resolving a given DNS name to a list of IPs.

It is build using operator SDK `operator-sdk`
and introduces a new CRD: `kind: DnsEndpoint`

## Installation

```
kubectl apply -f https://raw.githubusercontent.com/dmitrievav/dns-endpoint-operator/master/k8s/crd.yaml

kubectl apply -f https://raw.githubusercontent.com/dmitrievav/dns-endpoint-operator/master/k8s/deploy.yaml
```

## Usage examples

```
$ nslookup example.admitriev.eu

Non-authoritative answer:
Name:   example.admitriev.eu
Address: 10.10.10.1
Name:   example.admitriev.eu
Address: 10.10.10.2
```

```
$ cat <<EOF | kubectl apply -f -
apiVersion: k8s.admitriev.eu/v1
kind: DnsEndpoint
metadata:
  name: kafka-jmx-external
  labels:
    app: kafka-jmx-external
spec:
  dns: example.admitriev.eu
  port: 11001
  name: metrics
EOF
```

As result operator should create and update following resources for you:

```
$ kubectl get dnsendpoint,svc,endpoints
NAME                                              AGE
dnsendpoint.k8s.admitriev.eu/kafka-jmx-external   14m

NAME                         TYPE           CLUSTER-IP   EXTERNAL-IP            PORT(S)     AGE
service/kafka-jmx-external   ExternalName   <none>       example.admitriev.eu   11001/TCP   14m

NAME                           ENDPOINTS                           AGE
endpoints/kafka-jmx-external   10.10.10.1:11001,10.10.10.2:11001   14m
```

## Debug

```
kubectl logs -f deployment.apps/dns-endpoint-operator-controller-manager -n dns-endpoint-operator-system -c manager
```

## License

This project is licensed under the terms of the MIT license.
