//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

extern void goWindowClosed(void);

static NSWindow *window;
static BOOL terminating; // macOS asked to quit and waits for the answer

@interface Z407Delegate : NSObject <NSApplicationDelegate, NSWindowDelegate>
@end

@implementation Z407Delegate

// Dropping the web view with the window ends the page and its connections.
- (void)windowWillClose:(NSNotification *)note {
	window.contentView = nil;
	goWindowClosed();
}

// Cmd+Q, the Dock's Quit and logging out close the window like its close
// button. The Go side then stops the app in its own time (letting go of the
// speakers), and macwin_stop gives macOS the answer it waits for.
- (NSApplicationTerminateReply)applicationShouldTerminate:(NSApplication *)app {
	terminating = YES;
	[window close];
	return NSTerminateLater;
}

// A click on the Dock icon brings the window back after it was minimized.
- (BOOL)applicationShouldHandleReopen:(NSApplication *)app hasVisibleWindows:(BOOL)visible {
	[window makeKeyAndOrderFront:nil];
	return YES;
}

@end

static Z407Delegate *delegate; // NSApp and the window only hold it weakly

static void addItem(NSMenu *menu, NSString *title, SEL action, NSString *key) {
	[menu addItemWithTitle:title action:action keyEquivalent:key];
}

static NSMenu *submenu(NSMenu *bar, NSString *title) {
	NSMenuItem *item = [NSMenuItem new];
	NSMenu *menu = [[NSMenu alloc] initWithTitle:title];
	item.submenu = menu;
	[bar addItem:item];
	return menu;
}

// makeMenu builds the menu bar. The Edit menu is what makes copy and paste
// work in the page's text fields.
static NSMenu *makeMenu(NSString *name) {
	NSMenu *bar = [NSMenu new];
	NSMenu *app = submenu(bar, name);
	addItem(app, [@"Hide " stringByAppendingString:name], @selector(hide:), @"h");
	[app addItem:[NSMenuItem separatorItem]];
	addItem(app, [@"Quit " stringByAppendingString:name], @selector(terminate:), @"q");

	NSMenu *edit = submenu(bar, @"Edit");
	addItem(edit, @"Undo", @selector(undo:), @"z");
	addItem(edit, @"Redo", @selector(redo:), @"Z");
	[edit addItem:[NSMenuItem separatorItem]];
	addItem(edit, @"Cut", @selector(cut:), @"x");
	addItem(edit, @"Copy", @selector(copy:), @"c");
	addItem(edit, @"Paste", @selector(paste:), @"v");
	addItem(edit, @"Select All", @selector(selectAll:), @"a");

	NSMenu *win = submenu(bar, @"Window");
	addItem(win, @"Minimize", @selector(performMiniaturize:), @"m");
	addItem(win, @"Close", @selector(performClose:), @"w");
	return bar;
}

void macwin_run(const char *url, const char *title, int width, int height, const void *icon, int iconLen) {
	@autoreleasepool {
		NSApplication *app = [NSApplication sharedApplication];
		[app setActivationPolicy:NSApplicationActivationPolicyRegular];
		delegate = [Z407Delegate new];
		app.delegate = delegate;
		NSString *name = [NSString stringWithUTF8String:title];
		app.mainMenu = makeMenu(name);
		// Outside its app bundle the program has no icon of its own.
		if (icon != NULL && iconLen > 0 && NSBundle.mainBundle.bundleIdentifier == nil) {
			app.applicationIconImage = [[NSImage alloc] initWithData:[NSData dataWithBytes:icon length:iconLen]];
		}

		window = [[NSWindow alloc]
			initWithContentRect:NSMakeRect(0, 0, width, height)
			          styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
			                    NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable
			            backing:NSBackingStoreBuffered
			              defer:NO];
		window.releasedWhenClosed = NO;
		window.title = name;
		window.delegate = delegate;
		WKWebView *web = [[WKWebView alloc] initWithFrame:NSZeroRect configuration:[WKWebViewConfiguration new]];
		window.contentView = web;
		[web loadRequest:[NSURLRequest requestWithURL:[NSURL URLWithString:[NSString stringWithUTF8String:url]]]];
		[window center];
		[window setFrameAutosaveName:@"remote"]; // reopens where it was left
		[window makeKeyAndOrderFront:nil];
		if (@available(macOS 14, *)) {
			[app activate];
		} else {
			[app activateIgnoringOtherApps:YES];
		}
		[app run];
	}
}

void macwin_close(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[window close];
	});
}

// macwin_stop ends [NSApp run], or the process when macOS is waiting to
// quit it. The posted event wakes the run loop, which only notices the stop
// after handling an event.
void macwin_stop(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (terminating) {
			[NSApp replyToApplicationShouldTerminate:YES];
			return;
		}
		[NSApp stop:nil];
		NSEvent *wake = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
		                                   location:NSZeroPoint
		                              modifierFlags:0
		                                  timestamp:0
		                               windowNumber:0
		                                    context:nil
		                                    subtype:0
		                                      data1:0
		                                      data2:0];
		[NSApp postEvent:wake atStart:YES];
	});
}
