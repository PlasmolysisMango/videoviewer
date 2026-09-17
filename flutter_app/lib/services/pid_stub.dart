/// web 平台无进程概念：恒返 0，launcher 据此跳过 -parent 参数。
int currentProcessId() => 0;
