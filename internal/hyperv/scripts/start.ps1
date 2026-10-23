# script: start
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction Stop
if ($vm.State -ne 'Running') {
  Start-VM -Name $params.vmName
}
@{ state = [string](Get-VM -Name $params.vmName).State } | ConvertTo-Json -Compress
