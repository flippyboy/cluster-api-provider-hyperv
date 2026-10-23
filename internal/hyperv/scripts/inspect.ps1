# script: inspect
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction Stop
$nic = Get-VMNetworkAdapter -VMName $params.vmName | Select-Object -First 1
$ips = @()
if ($nic -and $nic.IPAddresses) {
  $ips = @($nic.IPAddresses)
}
@{
  vmID = [string]$vm.Id.Guid
  state = [string]$vm.State
  macAddress = [string]$nic.MacAddress
  ipAddresses = @($ips)
} | ConvertTo-Json -Compress
