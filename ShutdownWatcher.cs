using System;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.RegularExpressions;
using System.Windows.Forms;

internal static class Program {
    internal static StreamWriter Output;
    internal static int Result;
    [DllImport("kernel32.dll")] private static extern bool SetProcessShutdownParameters(uint level, uint flags);
    internal static void Log(string message) { try { Output.WriteLine(DateTime.UtcNow.ToString("o")+" "+message); } catch {} }
    [STAThread] private static int Main(string[] args) {
        try {
            Output=new StreamWriter(Console.OpenStandardOutput());Output.AutoFlush=true;
            if(args.Length<3)throw new ArgumentException("Expected local base URL, parent PID and token");
            Uri uri=new Uri(args[0]);int parentId=int.Parse(args[1]);
            if(uri.Scheme!="http"||uri.Host!="127.0.0.1"||!Regex.IsMatch(args[2],"^[0-9a-f]{64}$"))throw new ArgumentException("Invalid local listener arguments");
            SetProcessShutdownParameters(0x3ff,1);
            using(var form=new SessionWindow(uri.GetLeftPart(UriPartial.Authority),Process.GetProcessById(parentId),args[2],args.Length>3&&args[3]=="--self-test")) {
                Log("READY");Application.Run();
            }
        } catch(Exception error) { Log("ERROR "+error.Message);Result=1; }
        return Result;
    }
}
internal sealed class SessionWindow : Form {
    private readonly string endpoint,token;
    private readonly Process parent;
    private readonly System.Windows.Forms.Timer timer;
    private bool selfTest,sent;
    internal SessionWindow(string root,Process parentProcess,string secret,bool test) {
        endpoint=root+"/api/session-ending";parent=parentProcess;token=secret;selfTest=test;
        ShowInTaskbar=false;FormBorderStyle=FormBorderStyle.None;Text="Miku Screen Hub Session Watcher";
        IntPtr unused=Handle;
        timer=new System.Windows.Forms.Timer();timer.Interval=test?100:1000;
        timer.Tick+=delegate {
            if(selfTest){selfTest=false;try{RunSelfTest();}catch(Exception error){Program.Log("SELF_TEST_FAILED "+error.Message);Program.Result=1;Application.ExitThread();}return;}
            try { if(parent.HasExited)Application.ExitThread(); } catch { Application.ExitThread(); }
        };timer.Start();
    }
    protected override void SetVisibleCore(bool value) { base.SetVisibleCore(false); }
    protected override void WndProc(ref Message message) {
        if(message.Msg==0x0011) { message.Result=new IntPtr(1);return; }
        if(message.Msg==0x0016) {
            if(message.WParam!=IntPtr.Zero) {
                Notify((message.LParam.ToInt64()&0x80000000L)!=0?"logoff":"shutdown-or-restart");Application.ExitThread();
            } else Program.Log("CANCELLED: no display action");
            message.Result=IntPtr.Zero;return;
        }
        base.WndProc(ref message);
    }
    private void Notify(string reason) {
        if(sent)return;sent=true;
        try {
            var request=(HttpWebRequest)WebRequest.Create(endpoint);request.Proxy=null;request.Method="POST";
            request.Timeout=4200;request.ReadWriteTimeout=4200;request.ContentType="application/json";
            byte[] bytes=Encoding.UTF8.GetBytes("{\"token\":\""+token+"\",\"reason\":\""+reason+"\"}");request.ContentLength=bytes.Length;
            using(Stream stream=request.GetRequestStream())stream.Write(bytes,0,bytes.Length);
            using(var response=(HttpWebResponse)request.GetResponse())using(var reader=new StreamReader(response.GetResponseStream())) {
                string result=reader.ReadToEnd();if(result.Contains("\"ok\":false")){Program.Log("OFF_PARTIAL_FAILURE "+result);Program.Result=1;}else Program.Log("DISPLAYS_OFF "+(int)response.StatusCode);
            }
        } catch(Exception error) { Program.Log("OFF_FAILED "+error.Message);Program.Result=1; }
    }
    private void RunSelfTest() {
        Message query=Message.Create(Handle,0x0011,IntPtr.Zero,IntPtr.Zero);WndProc(ref query);
        if(query.Result!=new IntPtr(1))throw new Exception("Query rejected shutdown");
        Message cancel=Message.Create(Handle,0x0016,IntPtr.Zero,IntPtr.Zero);WndProc(ref cancel);
        if(sent)throw new Exception("Cancelled shutdown powered off displays");
        Message end=Message.Create(Handle,0x0016,new IntPtr(1),IntPtr.Zero);WndProc(ref end);
        if(!sent)throw new Exception("Confirmed session end was not handled");
        Program.Log("SELF_TEST_COMPLETE");
    }
    protected override void Dispose(bool disposing) { if(disposing){timer.Stop();timer.Dispose();parent.Dispose();}base.Dispose(disposing); }
}
