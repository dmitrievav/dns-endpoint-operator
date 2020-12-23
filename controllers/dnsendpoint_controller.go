/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"net"
	"reflect"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k8sv1 "github.com/dmitrievav/dns-endpoint-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	sleepTime = 30 * time.Second
)

// DnsEndpointReconciler reconciles a DnsEndpoint object
type DnsEndpointReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// The ClusterRole manifest at config/rbac/role.yaml
// is generated from the above markers via controller-gen
// with the following command:
// $ make manifests

// +kubebuilder:rbac:groups=k8s.admitriev.eu,resources=dnsendpoints,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k8s.admitriev.eu,resources=dnsendpoints/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core,resources=services;endpoints,verbs=get;list;watch;create;update;patch;delete

func (r *DnsEndpointReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {

	ctx := context.Background()
	log := r.Log.WithValues("dnsendpoint", req.NamespacedName)

	// Fetch the Dnsendpoint instance
	dnsendpoint := &k8sv1.DnsEndpoint{}
	err := r.Get(ctx, req.NamespacedName, dnsendpoint)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("DnsEndpoint resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get DnsEndpoint")
		return ctrl.Result{}, err
	}

	// Check if the service already exists, if not create a new one
	svc := &corev1.Service{}
	err = r.Get(ctx, req.NamespacedName, svc)
	if err != nil {
		if errors.IsNotFound(err) {
			// Define a new service
			svc = r.serviceForDnsEndpoint(dnsendpoint)
			log.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			err = r.Create(ctx, svc)
			if err != nil {
				log.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
				return ctrl.Result{}, err
			}
			// Service created successfully - return and requeue
			// return ctrl.Result{Requeue: true}, nil
		} else {
			log.Error(err, "Failed to get Service")
			return ctrl.Result{}, err
		}
	}

	// Resolve DNS and prepare EndpointAddress
	var ips []string
	var addresses []corev1.EndpointAddress
	err = sortedNsLookup(dnsendpoint.Spec.Dns, &ips)
	if err != nil {
		log.Error(err, "Could not resolve DNS")
		// Could not resolve DNS - return and requeue after sleepTime
		return ctrl.Result{RequeueAfter: sleepTime}, err
	}
	for _, ip := range ips {
		addresses = append(addresses, corev1.EndpointAddress{IP: ip})
	}
	log.Info("DNS resolve", "Host", dnsendpoint.Spec.Dns, "IPs", ips)

	// Create endpoints
	endpoints := &corev1.Endpoints{}
	err = r.Get(ctx, req.NamespacedName, endpoints)
	if err != nil {
		if errors.IsNotFound(err) {
			// Define new endpoints
			endpoints = r.endpointsForDnsEndpoint(dnsendpoint, addresses)
			log.Info("Creating new Epoints", "Endpoints.Namespace", endpoints.Namespace, "Endpoints.Name", endpoints.Name)
			err = r.Create(ctx, endpoints)
			if err != nil {
				log.Error(err, "Failed to create new Epoints", "Endpoints.Namespace", endpoints.Namespace, "Endpoints.Name", endpoints.Name)
				return ctrl.Result{}, err
			}
			dnsendpoint.Status.Ips = ips
			log.Info("Update DnsEpoint status", "DnsEpoint.Namespace", dnsendpoint.Namespace, "DnsEpoint.Name", dnsendpoint.Name)
			err := r.Status().Update(ctx, dnsendpoint)
			if err != nil {
				log.Error(err, "Failed to update DnsEpoint status")
				return ctrl.Result{}, err
			}
			// Epoints created successfully - return and requeue after sleepTime
			return ctrl.Result{RequeueAfter: sleepTime}, nil
		}
		log.Error(err, "Failed to get Epoints")
		return ctrl.Result{}, err
	}
	log.Info("DNS cache", "Host", dnsendpoint.Spec.Dns, "IPs", dnsendpoint.Status.Ips)

	// If IPs are changed, update endpoints and status
	if !reflect.DeepEqual(dnsendpoint.Status.Ips, ips) {
		endpoints.Subsets[0].Addresses = addresses
		log.Info("Update Epoints", "endpoints.Namespace", endpoints.Namespace, "endpoints.Name", endpoints.Name)
		err = r.Update(ctx, endpoints)
		if err != nil {
			log.Error(err, "Failed to update Endpoints", "Endpoints.Namespace", endpoints.Namespace, "Endpoints.Name", endpoints.Name)
			return ctrl.Result{}, err
		}
		dnsendpoint.Status.Ips = ips
		log.Info("Update DnsEpoint status", "DnsEpoint.Namespace", dnsendpoint.Namespace, "DnsEpoint.Name", dnsendpoint.Name)
		err := r.Status().Update(ctx, dnsendpoint)
		if err != nil {
			log.Error(err, "Failed to update DnsEpoint status")
			return ctrl.Result{}, err
		}
	}
	// Epoints updated - return and requeue after sleepTime
	return ctrl.Result{RequeueAfter: sleepTime}, nil
}

// serviceForDnsEndpoint returns a dnsendpoint Service object
func (r *DnsEndpointReconciler) serviceForDnsEndpoint(d *k8sv1.DnsEndpoint) *corev1.Service {
	ls := labelsForDnsEndpoint(d.Name)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name,
			Namespace: d.Namespace,
			Labels:    ls,
		},
		Spec: corev1.ServiceSpec{
			Type:         "ExternalName",
			ExternalName: d.Spec.Dns,
			Ports: []corev1.ServicePort{{
				Name: d.Spec.Name,
				Port: d.Spec.Port,
			}},
		},
	}
	// Set DnsEndpoint instance as the owner and controller
	ctrl.SetControllerReference(d, svc, r.Scheme)
	return svc
}

// endpointsForDnsEndpoint returns a DnsEndpoint Endpoints object
func (r *DnsEndpointReconciler) endpointsForDnsEndpoint(d *k8sv1.DnsEndpoint, addresses []corev1.EndpointAddress) *corev1.Endpoints {
	ls := labelsForDnsEndpoint(d.Name)

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      d.Name,
			Namespace: d.Namespace,
			Labels:    ls,
		},
		Subsets: []corev1.EndpointSubset{{
			Addresses: addresses,
			Ports: []corev1.EndpointPort{{
				Name: d.Spec.Name,
				Port: d.Spec.Port,
			}},
		}},
	}
	// Set DnsEndpoint instance as the owner and controller
	ctrl.SetControllerReference(d, endpoints, r.Scheme)
	return endpoints
}

// labelsForDnsEndpoint returns the labels for selecting the resources
// belonging to the given dnsendpoint CR name.
func labelsForDnsEndpoint(name string) map[string]string {
	return map[string]string{"app": name, "app.kubernetes.io/part-of": "dnsendpoint"}
}

// Resolve DNS name to sorted IP list
func sortedNsLookup(host string, ips *[]string) error {
	netIPs, err := net.LookupIP(host)
	if err != nil {
		return err
	}
	for _, netIP := range netIPs {
		*ips = append(*ips, netIP.String())
	}
	sort.Strings(*ips)
	return nil
}

func (r *DnsEndpointReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k8sv1.DnsEndpoint{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Endpoints{}).
		Complete(r)
}
