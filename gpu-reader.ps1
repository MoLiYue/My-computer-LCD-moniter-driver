param([switch]$Once)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
# DXGI provides 64-bit memory sizes and an adapter LUID to match Windows GPU counters.
Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
public static class DisplayGpuInfo {
    [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
    public struct Desc {
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst=128)] public string Description;
        public uint VendorId, DeviceId, SubSysId, Revision;
        public UIntPtr DedicatedVideoMemory, DedicatedSystemMemory, SharedSystemMemory;
        public uint LuidLow, LuidHigh, Flags;
    }
    [DllImport("dxgi.dll")] static extern int CreateDXGIFactory1(ref Guid iid, out IntPtr factory);
    [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int EnumAdapter(IntPtr self, uint index, out IntPtr adapter);
    [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int ReadDesc(IntPtr self, out Desc desc);
    static Delegate Method(IntPtr instance, int index, Type signature) {
        return Marshal.GetDelegateForFunctionPointer(Marshal.ReadIntPtr(Marshal.ReadIntPtr(instance),index*IntPtr.Size),signature);
    }
    public static Desc[] Read() {
        Guid iid=new Guid("770aae78-f26f-4dba-a829-253c83d1b387"); IntPtr factory;
        Marshal.ThrowExceptionForHR(CreateDXGIFactory1(ref iid,out factory));
        var result=new List<Desc>();
        try {
            var enumerate=(EnumAdapter)Method(factory,12,typeof(EnumAdapter));
            for(uint i=0;i<64;i++) {
                IntPtr adapter; int status=enumerate(factory,i,out adapter);
                if(status==unchecked((int)0x887A0002))break;
                Marshal.ThrowExceptionForHR(status);
                try { Desc desc; Marshal.ThrowExceptionForHR(((ReadDesc)Method(adapter,10,typeof(ReadDesc)))(adapter,out desc)); result.Add(desc); }
                finally { Marshal.Release(adapter); }
            }
        } finally { Marshal.Release(factory); }
        return result.ToArray();
    }
}
'@
$activeNames = @(Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUAdapterMemory | Select-Object -ExpandProperty Name)
$adapter = [DisplayGpuInfo]::Read() | Where-Object { ($_.Flags -band 2) -eq 0 -and $_.DedicatedVideoMemory.ToUInt64() -gt 0 -and $activeNames -contains ('luid_0x{0:x8}_0x{1:x8}_phys_0' -f $_.LuidHigh,$_.LuidLow) } | Sort-Object { $_.DedicatedVideoMemory.ToUInt64() } -Descending | Select-Object -First 1
if (-not $adapter) { throw 'No hardware GPU with dedicated memory was found' }
$luid = 'luid_0x{0:x8}_0x{1:x8}' -f $adapter.LuidHigh,$adapter.LuidLow
do {
    try {
        $engines = @(Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUEngine -Filter "Name LIKE '%${luid}%'" | Where-Object { $_.Name -match '_phys_0_eng_' })
        $memory = @(Get-CimInstance Win32_PerfFormattedData_GPUPerformanceCounters_GPUAdapterMemory -Filter "Name LIKE '${luid}_phys_0'")
        if ($engines.Count -eq 0 -or $memory.Count -ne 1) { throw 'GPU counters are unavailable' }
        # Sum process counters for each physical engine, then select the busiest engine.
        $loads = @($engines | Group-Object { $_.Name -replace '^pid_\d+_', '' } | ForEach-Object { ($_.Group | Measure-Object -Property UtilizationPercentage -Sum).Sum })
        $used = [double]$memory[0].DedicatedUsage
        $total = [double]$adapter.DedicatedVideoMemory.ToUInt64()
        @{ gpuName=$adapter.Description; gpu=[Math]::Min(100.0,[Math]::Max(0.0,($loads | Measure-Object -Maximum).Maximum)); vramUsedBytes=$used; vramTotalBytes=$total; vram=[Math]::Min(100.0,100*$used/$total); source='Windows GPU counters / DXGI'; error=$null } | ConvertTo-Json -Compress
    } catch {
        @{ gpuName=$adapter.Description; gpu=$null; vramUsedBytes=$null; vramTotalBytes=$null; vram=$null; source='Windows GPU counters / DXGI'; error=$_.Exception.Message } | ConvertTo-Json -Compress
    }
    if (-not $Once) { Start-Sleep -Seconds 2 }
} while (-not $Once)
