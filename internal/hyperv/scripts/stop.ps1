# script: stop
Import-Module Hyper-V -ErrorAction Stop
$vm = Get-VM -Name $params.vmName -ErrorAction SilentlyContinue
if ($vm -and $vm.State -ne 'Off') {
  Stop-VM -Name $params.vmName -TurnOff -Force
}
@{ state = 'Off' } | ConvertTo-Json -Compress
