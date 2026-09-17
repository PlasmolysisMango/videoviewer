/// 当前进程 PID 获取的平台条件导出。
/// IO 平台（桌面/移动）走 FFI 实现；web 编译时选 stub（恒返 0，不传 -parent）。
export 'pid_stub.dart' if (dart.library.io) 'pid_ffi.dart';
