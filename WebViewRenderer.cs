using System;
using System.Drawing;
using System.Globalization;
using System.IO;
using System.Text;
using System.Threading.Tasks;
using System.Windows.Forms;
using Microsoft.Web.WebView2.Core;

internal static class WebViewRenderer
{
    private static StreamWriter output;
    private static int result;

    [STAThread]
    private static int Main(string[] args)
    {
        output = new StreamWriter(Console.OpenStandardOutput(), new UTF8Encoding(false)) { AutoFlush = true };
        try
        {
            if (args.Length != 4) throw new ArgumentException("Expected profile, data directory, width and height");
            var window = new Form { ShowInTaskbar = false, FormBorderStyle = FormBorderStyle.None };
            using (window)
            using (var context = new ApplicationContext())
            {
                IntPtr parent = window.Handle;
                window.BeginInvoke(new Action(async () => {
                    try { await RenderAsync(parent, args); }
                    catch (Exception error) { ReportError(error); result = 1; }
                    finally { context.ExitThread(); }
                }));
                Application.Run(context);
            }
        }
        catch (Exception error) { ReportError(error); result = 1; }
        return result;
    }

    private static async Task RenderAsync(IntPtr parent, string[] args)
    {
        int width = int.Parse(args[2], CultureInfo.InvariantCulture);
        int height = int.Parse(args[3], CultureInfo.InvariantCulture);
        string source = await Task.Run(() => Console.ReadLine());
        if (source == null) return;
        var url = new Uri(source);
        if ((url.Scheme != "http" && url.Scheme != "https") || width < 1 || height < 1 || width > 4096 || height > 4096)
            throw new ArgumentException("Invalid page URL or dimensions");
        var environment = await CoreWebView2Environment.CreateAsync(null, args[1],
            new CoreWebView2EnvironmentOptions("--disable-background-timer-throttling --disable-renderer-backgrounding --disable-backgrounding-occluded-windows"));
        var options = environment.CreateCoreWebView2ControllerOptions();
        options.ProfileName = args[0];
        var controller = await environment.CreateCoreWebView2ControllerAsync(parent, options);
        try
        {
            controller.ShouldDetectMonitorScaleChanges = false;
            controller.RasterizationScale = 1;
            controller.Bounds = new Rectangle(0, 0, width, height);
            controller.IsVisible = true;
            var view = controller.CoreWebView2;
            view.Settings.IsScriptEnabled = true;
            view.Settings.IsWebMessageEnabled = false;
            view.Settings.AreHostObjectsAllowed = false;
            view.NewWindowRequested += (sender, evt) => { evt.Handled = true; };
            view.PermissionRequested += (sender, evt) => { evt.State = CoreWebView2PermissionState.Deny; };
            view.DownloadStarting += (sender, evt) => { evt.Cancel = true; };
            view.NavigationStarting += (sender, evt) => {
                Uri target;
                if (!Uri.TryCreate(evt.Uri, UriKind.Absolute, out target) || (target.Scheme != "http" && target.Scheme != "https"))
                    evt.Cancel = true;
            };
            var nav = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
            EventHandler<CoreWebView2NavigationCompletedEventArgs> navigated = (sender, evt) => {
                if (evt.IsSuccess) nav.TrySetResult(true);
                else nav.TrySetException(new InvalidOperationException("Page navigation failed: " + evt.WebErrorStatus));
            };
            view.NavigationCompleted += navigated;
            view.Navigate(url.AbsoluteUri);
            if (await Task.WhenAny(nav.Task, Task.Delay(10000)) != nav.Task) throw new TimeoutException("WebView2 navigation timed out");
            await nav.Task;
            view.NavigationCompleted -= navigated;
            output.WriteLine("READY");

            while (true)
            {
                string line = await Task.Run(() => Console.ReadLine());
                if (line == null || line == "QUIT") return;
                int quality;
                if (!line.StartsWith("CAPTURE ", StringComparison.Ordinal) || !int.TryParse(line.Substring(8), out quality) || quality < 0 || quality > 100)
                    throw new InvalidOperationException("Unknown capture request");
                string response = await view.CallDevToolsProtocolMethodAsync("Page.captureScreenshot",
                    "{\"format\":\"jpeg\",\"quality\":" + quality.ToString(CultureInfo.InvariantCulture) + ",\"fromSurface\":true}");
                output.WriteLine("FRAME " + response);
            }
        }
        finally { controller.Close(); }
    }

    private static void ReportError(Exception error)
    {
        try { output.WriteLine("ERROR " + Convert.ToBase64String(Encoding.UTF8.GetBytes(error.GetBaseException().Message))); }
        catch (IOException) { }
    }
}
