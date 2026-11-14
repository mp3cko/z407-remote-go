//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <IOKit/pwr_mgt/IOPMLib.h>
#import <IOKit/IOMessage.h>

extern void goPowerEvent(int what);

static io_connect_t root;
static IONotificationPortRef port;
static io_object_t notifier;

// The system waits for IOAllowPowerChange before it sleeps (up to 30 s).
static void powerChanged(void *ref, io_service_t service, natural_t type, void *arg) {
	switch (type) {
	case kIOMessageCanSystemSleep: // idle sleep: never held up
		IOAllowPowerChange(root, (long)arg);
		break;
	case kIOMessageSystemWillSleep:
		goPowerEvent(1);
		IOAllowPowerChange(root, (long)arg);
		break;
	case kIOMessageSystemHasPoweredOn:
		goPowerEvent(2);
		break;
	}
}

int power_watch(void) {
	root = IORegisterForSystemPower(NULL, &port, powerChanged, &notifier);
	if (root == MACH_PORT_NULL) {
		return -1;
	}
	// A queue of its own: the main thread may not run a run loop (--no-window).
	IONotificationPortSetDispatchQueue(port, dispatch_queue_create("z407.power", DISPATCH_QUEUE_SERIAL));
	return 0;
}

static id activity; // held for the life of the process

void power_no_nap(void) {
	if (activity == nil) {
		activity = [[NSProcessInfo processInfo]
			beginActivityWithOptions:NSActivityUserInitiatedAllowingIdleSystemSleep
			                  reason:@"Serving the remote and holding the speakers"];
	}
}
