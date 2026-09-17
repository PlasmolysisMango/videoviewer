import 'dart:ffi';

/// 通过 kernel32.GetCurrentProcessId 取当前进程 PID（仅 Windows 调用）。
int currentProcessId() {
  final kernel32 = DynamicLibrary.open('kernel32.dll');
  final getCurrentProcessId = kernel32
      .lookupFunction<Uint32 Function(), int Function()>('GetCurrentProcessId');
  return getCurrentProcessId();
}
