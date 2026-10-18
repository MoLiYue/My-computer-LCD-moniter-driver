$ErrorActionPreference = 'Stop'
$sensorService = $null
$sensorId = $null
$sensorName = $null
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
while ($true) {
    try {
        if ($null -eq $sensorService) {
            $sensorService = New-Object -ComObject 'atkexCom.axdata'
            $temperatureItems = @($sensorService.ItemsOfGroup([uint32]6))
            $selectedSensor = $temperatureItems | Where-Object name -eq 'CPU Package' | Select-Object -First 1
            if ($null -eq $selectedSensor) {
                $selectedSensor = $temperatureItems | Where-Object name -eq 'CPU' | Select-Object -First 1
            }
            if ($null -eq $selectedSensor) { throw 'ASUS CPU temperature sensor is unavailable' }
            $sensorId = [uint32]$selectedSensor.id
            $sensorName = [string]$selectedSensor.name
        }
        $rawTemperature = [uint32]0
        $result = $sensorService.iAcpiGetItem($sensorId, [ref]$rawTemperature)
        if ($result -ne 1) { throw 'ASUS CPU temperature read failed' }
        $temperatureC = $rawTemperature / 10.0
        if ($temperatureC -lt 5 -or $temperatureC -gt 125) { throw 'CPU temperature is outside the sensor range' }
        [Console]::WriteLine((@{ cpuTemperature = $temperatureC; source = "ASUS ACPI / $sensorName" } | ConvertTo-Json -Compress))
    } catch {
        [Console]::WriteLine((@{ cpuTemperature = $null; source = 'ASUS ACPI'; error = $_.Exception.Message } | ConvertTo-Json -Compress))
        $sensorService = $null
    }
    Start-Sleep -Seconds 2
}
