# script: ensure-vm
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction SilentlyContinue
if (-not $vm) {
  $path = [string]$params.vmPath
  if ([string]::IsNullOrWhiteSpace($path)) {
    $path = (Get-VMHost).VirtualMachinePath
  }
  $dir = Join-Path $path $params.vmName
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
  $memory = [int64]$params.memoryMiB * 1MB
  New-VM -Name $params.vmName -Generation 2 -MemoryStartupBytes $memory -NoVHD -SwitchName $params.virtualSwitch -Path $path | Out-Null
  Set-VM -Name $params.vmName -ProcessorCount ([int]$params.vCPUs) -StaticMemory -MemoryStartupBytes $memory -DynamicMemoryEnabled $false
  Set-VMFirmware -VMName $params.vmName -EnableSecureBoot Off
  $mac = ([string]$params.macAddress) -replace '[:\-\.]', ''
  Set-VMNetworkAdapter -VMName $params.vmName -StaticMacAddress $mac.ToUpper()
  $vm = Get-VM -Name $params.vmName
}
$nic = Get-VMNetworkAdapter -VMName $params.vmName | Select-Object -First 1
@{
  vmID = [string]$vm.Id.Guid
  vmName = [string]$vm.Name
  macAddress = [string]$nic.MacAddress
  vmPath = [string]$vm.Path
  state = [string]$vm.State
} | ConvertTo-Json -Compress
