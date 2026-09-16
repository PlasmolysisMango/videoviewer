// 基本 Widget 冒烟测试：App 能否启动并渲染出 Material 根组件。
//
// 说明：MyApp 需要 API client；测试用不可达地址，仅验证 UI 启动不发真实请求。
// SharedPreferences 由 Provider（主题/登录态）使用，测试环境需注入 mock 初值。

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:javdb_app/api/client.dart';
import 'package:javdb_app/main.dart';

void main() {
  testWidgets('app boots and renders MaterialApp', (WidgetTester tester) async {
    SharedPreferences.setMockInitialValues(<String, Object>{});
    await tester.pumpWidget(
      MyApp(client: JavDBClient('http://127.0.0.1:1')),
    );
    await tester.pump();
    expect(find.byType(MaterialApp), findsOneWidget);
  });
}
