# cluster-api-provider-hyperv

Cluster API infrastructure provider for a single Windows Hyper-V host. It creates Generation 2 Linux virtual machines over SSH or WinRM. The clusterctl provider name is `hyperv`.

Bootstrap and control plane stay upstream: `KubeadmConfig` and `KubeadmControlPlane`. The stored API is `infrastructure.cluster.x-k8s.io/v1alpha1`, and it implements the Cluster API v1beta2 contract.

## Install the manager

The provider namespace is `hyperv-system`. The manager image is `ghcr.io/flippyboy/cluster-api-provider-hyperv`. Use clusterctl or the Helm chart, not both. They install the same objects.

### clusterctl

clusterctl reads the GitHub release assets `infrastructure-components.yaml`, `metadata.yaml`, and `cluster-template.yaml`. Add the repository to `~/.cluster-api/clusterctl.yaml`:

```yaml
providers:
  - name: hyperv
    url: https://github.com/flippyboy/cluster-api-provider-hyperv/releases/latest/infrastructure-components.yaml
    type: InfrastructureProvider
```

```sh
clusterctl init --infrastructure hyperv
```

That also installs core Cluster API, kubeadm bootstrap, and kubeadm control plane when they are not already present. The release URL works after the first `vX.Y.Z` tag. Until then, install from a checkout with Helm or `kubectl apply -k config/default`.

`make release` writes those assets to `out/`. A `vX.Y.Z` tag also writes `infrastructure-hyperv-<tag>-components.yaml` and `infrastructure-hyperv-<tag>-metadata.yaml`. The release workflow publishes the assets and pushes `ghcr.io/flippyboy/cluster-api-provider-hyperv:<tag>`.

### Helm

The chart is `charts/cluster-api-provider-hyperv`. It templates the clusterctl manifest. `image.repository`, `image.tag`, `image.pullPolicy`, `replicaCount`, and `leaderElection` are values. Install it in `hyperv-system`.

```sh
helm install cluster-api-provider-hyperv charts/cluster-api-provider-hyperv \
  --namespace hyperv-system \
  --create-namespace
```

`make helm` lints, renders, and packages the chart. Each GitHub release includes the packaged chart, with `image.tag` set to that release.

### Development

```sh
kubectl apply -k config/default
```

That applies the working tree. The image tag in that overlay is `latest`.

The process accepts `--namespace` and `--watch-filter` so clusterctl can limit the cache. An empty namespace watches every namespace. Leader election uses the lease `hyperv.cluster.x-k8s.io`.

## Prepare the Hyper-V host

Install the Hyper-V role. Create an external virtual switch and leave it in place. The provider does not create the switch.

The account must be a local Administrator. `Administrator` and `HOST\Administrator` are both accepted.

### SSH

SSH is the default transport.

- Install the OpenSSH Server feature and allow TCP 22 through the firewall.
- Put an unencrypted OpenSSH or PEM private key in the credentials Secret. Encrypted keys are rejected. There is no passphrase support.
- `ssh-known-hosts` holds the host key line. Use `hostname` when the port is 22, and `[hostname]:port` for any other port.
- `host.insecureSkipHostKeyVerification` skips that check.

ISO upload uses SFTP. OpenSSH on Windows accepts forward-slash paths. The controller converts `\` to `/` for that upload. Spec fields stay Windows paths.

### WinRM

Set `spec.host.transport` to `WinRM`. `spec.host.winRMScheme` is `HTTPS` (port 5986) or `HTTP` (port 5985). `spec.host.port` overrides the port. `0` means the default for the selected transport.

- Run `Enable-PSRemoting -Force` or `winrm quickconfig`.
- Allow the listener port through the firewall.
- Leave Negotiate and NTLM enabled.
- Do not enable Basic authentication.
- Do not set `AllowUnencrypted`. NTLM message encryption seals the SOAP body, including on the HTTP listener.

For HTTPS, bind a certificate to the listener. Optional Secret key `ca-cert` is a PEM bundle used to verify that certificate. `spec.host.insecureSkipTLSVerify` skips verification. `spec.host.tlsServerName` is the name to check when `address` is an IP and the certificate is issued to a DNS name.

There is no SFTP on WinRM. The controller writes the NoCloud ISO in 32KiB base64 chunks.

Each PowerShell command has a two-minute timeout. A `Copy` clone of a large VHDX has to finish inside that window. Differencing disks do not copy the parent.

## Credentials

Create the Secret in the same namespace as the `HyperVCluster`. `spec.credentialsSecretRef.name` points at it.

SSH:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: hyperv-creds
  namespace: default
stringData:
  username: Administrator
  ssh-privatekey: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    ...
    -----END OPENSSH PRIVATE KEY-----
  ssh-known-hosts: "hyperv.example ssh-ed25519 AAAA..."
```

WinRM:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: hyperv-creds
  namespace: default
stringData:
  username: HOST\Administrator
  password: change-me
  # ca-cert is optional, HTTPS only
  ca-cert: |
    -----BEGIN CERTIFICATE-----
    ...
    -----END CERTIFICATE-----
```

The controller does not log private keys or passwords.

## Golden image

`spec.imagePath` is a Generation 2 VHDX that already exists on the host. The guest is Linux, with cloud-init and the in-kernel Hyper-V drivers (`hv_vmbus`, `hv_netvsc`, `hv_storvsc`, `hv_utils`). The provider turns Secure Boot off. The image must boot that way. Generation and Secure Boot are not spec fields.

The default disk is a differencing VHDX whose parent is that image. The parent must stay immutable. The controller never deletes it, and VM deletion refuses to remove the directory that contains it. Set `spec.disk.cloneMode` to `Copy` for a full copy. Optional `spec.disk.sizeGiB` grows the disk with `Resize-VHD` when the requested size is larger. Disks are never shrunk.

A machine may override `imagePath` and `virtualSwitch`.

## Control plane endpoint

Every guest uses DHCP. You set `Cluster.spec.controlPlaneEndpoint` yourself. The provider does not allocate that address. Use a DHCP reservation, an external load balancer, or `spec.kubeVIP` below.

The `HyperVCluster` becomes provisioned after the host probe succeeds and the endpoint host and port are both set. The provider does not wait for the endpoint to answer. A later host failure sets `Ready` to False and leaves `provisioned` set.

### kube-vip

Set `spec.kubeVIP.enabled` to install [kube-vip](https://kube-vip.io/) on control plane machines. The virtual IP and port are `Cluster.spec.controlPlaneEndpoint`. Workers do not run it. Leave it disabled when that endpoint is a DHCP reservation or an external load balancer.

The static pod uses ARP leader election and binds the address to `spec.kubeVIP.interface` (`eth0` by default). Cloud-init renames the matched NIC to that name. The VIP is a second address on the guest NIC, so the external virtual switch has to forward gratuitous ARP. Service load balancers are not configured.

The image defaults to `ghcr.io/kube-vip/kube-vip:v1.3.0`. On the first control plane node, kube-vip waits for kubeadm's `super-admin.conf`. Nodes that join later use `admin.conf`. The guest still needs to pull that image and `busybox:1.37.0` before `kubeadm init` finishes.

`HYPERV_KUBE_VIP` in the cluster template sets `spec.kubeVIP.enabled`. `HYPERV_KUBE_VIP_INTERFACE` sets the guest interface.

The controller waits to mark a machine provisioned until Hyper-V integration services report a non-link-local IPv4 address. `providerID` is `hyperv://<vm-guid>`. It is injected into the kubeadm `InitConfiguration` or `JoinConfiguration` before the VM starts, for kubeadm v1beta3 maps and v1beta4 name/value lists. The kubelet is not configured with `cloud-provider: external`. There is no Hyper-V cloud provider.

MAC addresses are assigned by the controller and matched in the NoCloud netplan (`dhcp4: true`). An existing VM keeps its CPU, memory, and MAC.

## Create a workload cluster

`metadata.yaml` declares release series `0.1` and contract `v1beta2` for clusterctl. The template is `templates/cluster-template.yaml`. Values written as `${NAME:=default}` are clusterctl defaults.

With the provider initialized:

```sh
clusterctl generate cluster "$CLUSTER_NAME" --infrastructure hyperv > cluster.yaml
```

clusterctl substitutes the variables below from the environment. The credentials Secret must already exist. The endpoint is written on `Cluster.spec.controlPlaneEndpoint` and on `kubeadmConfigSpec.clusterConfiguration.controlPlaneEndpoint`.

| Variable | Meaning |
| --- | --- |
| `CLUSTER_NAME` | Cluster and object name |
| `KUBERNETES_VERSION` | kubeadm Kubernetes version |
| `CONTROL_PLANE_ENDPOINT_IP` | API server host or reserved IP |
| `CONTROL_PLANE_ENDPOINT_PORT` | API server port, default `6443` |
| `CONTROL_PLANE_MACHINE_COUNT` | Control plane replicas, default `1` |
| `WORKER_MACHINE_COUNT` | Worker replicas, default `1` |
| `HYPERV_HOST` | Hyper-V host name or IP |
| `HYPERV_PORT` | TCP port. `0` uses 22, 5985, or 5986 |
| `HYPERV_TRANSPORT` | `SSH` (default) or `WinRM` |
| `HYPERV_WINRM_SCHEME` | `HTTPS` (default) or `HTTP` |
| `HYPERV_CREDENTIALS_SECRET` | Secret name in the cluster namespace |
| `HYPERV_VIRTUAL_SWITCH` | Existing external switch |
| `HYPERV_IMAGE_PATH` | Golden VHDX path on the host |
| `HYPERV_VCPUS` | vCPU count for control plane and workers, default `2` |
| `HYPERV_MEMORY_MIB` | Static memory in MiB for both, default `2048` |
| `HYPERV_KUBE_VIP` | Install kube-vip on control plane machines, default `false` |
| `HYPERV_KUBE_VIP_INTERFACE` | Guest interface that holds the virtual IP, default `eth0` |

VM names are `capi-<cluster>-<machine>`, sanitized and truncated to 100 characters. Once `status.vmName` is stored, that name is reused. Several workload clusters can share one host. Each `HyperVCluster` has its own host address and Secret.

## Out of scope

One host only. No failover cluster, SCVMM, live migration, Windows guests, ClusterClass, MachinePool, load balancer, static IPAM, snapshots, CPU or memory resize after create, GPU, or image build.
