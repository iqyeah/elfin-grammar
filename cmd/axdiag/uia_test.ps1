$log = "C:\Users\vboxuser\Library\Logs\uia_ps.log"
"=== UIA PowerShell test $(Get-Date) ===" | Out-File -Append $log -Encoding utf8
try {
    Add-Type -AssemblyName UIAutomationClient
    "Add-Type OK" | Out-File -Append $log -Encoding utf8
    $root = [System.Windows.Automation.AutomationElement]::RootElement
    "RootElement OK: name=[$($root.Current.Name)]" | Out-File -Append $log -Encoding utf8
    $f = [System.Windows.Automation.AutomationElement]::FocusedElement
    "FocusedElement OK: name=[$($f.Current.Name)] ctype=[$($f.Current.ControlType.ProgrammaticName)]" | Out-File -Append $log -Encoding utf8
} catch {
    "EXCEPTION: $_" | Out-File -Append $log -Encoding utf8
    "EXCEPTION Type: $($_.Exception.GetType().FullName)" | Out-File -Append $log -Encoding utf8
    if ($_.Exception.InnerException) {
        "INNER: $($_.Exception.InnerException.Message)" | Out-File -Append $log -Encoding utf8
    }
}
"=== done ===" | Out-File -Append $log -Encoding utf8
