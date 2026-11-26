# script: ensure-vm
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction SilentlyContinue
$memory = [int64]$params.memoryMiB * 1MB
if (-not $vm) {
  $path = [string]$params.vmPath
  if ([string]::IsNullOrWhiteSpace($path)) {
    $path = (Get-VMHost).VirtualMachinePath
  }
  $dir = Join-Path $path $params.vmName
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  New-VM -Name $params.vmName -Generation 2 -MemoryStartupBytes $memory -NoVHD -SwitchName $params.virtualSwitch -Path $path | Out-Null
  $vm = Get-VM -Name $params.vmName
}
# Generation 2 defaults to dynamic memory and Secure Boot. This host's Set-VM
# has -StaticMemory, matching taliesins/hyperv, and no -DynamicMemoryEnabled.
# Apply it whenever the VM is off so a failed first pass can be finished.
if ($vm.State -eq 'Off') {
  Set-VM -Name $params.vmName -ProcessorCount ([int]$params.vCPUs) -StaticMemory -MemoryStartupBytes $memory -CheckpointType Disabled
  Set-VMFirmware -VMName $params.vmName -EnableSecureBoot Off
  $mac = ([string]$params.macAddress) -replace '[:\-\.]', ''
  if ($mac -match '^[0-9A-Fa-f]{12}$' -and $mac -ne '000000000000') {
    Set-VMNetworkAdapter -VMName $params.vmName -StaticMacAddress $mac.ToUpper()
  }
  $vm = Get-VM -Name $params.vmName
}
$adapter = Get-VMNetworkAdapter -VMName $params.vmName | Select-Object -First 1
$vlan = 0
if ($null -ne $params.vlanID) {
  $vlan = [int]$params.vlanID
}
if ($vlan -eq 0) {
  Set-VMNetworkAdapterVlan -VMNetworkAdapter $adapter -Untagged | Out-Null
} elseif ($vlan -ge 1 -and $vlan -le 4094) {
  Set-VMNetworkAdapterVlan -VMNetworkAdapter $adapter -Access -VlanId $vlan | Out-Null
} else {
  throw "vlanID $vlan is outside 0 through 4094"
}
@{
  vmID = [string]$vm.Id.Guid
  vmName = [string]$vm.Name
  macAddress = [string]$adapter.MacAddress
  vmPath = [string]$vm.Path
  state = [string]$vm.State
} | ConvertTo-Json -Compress
