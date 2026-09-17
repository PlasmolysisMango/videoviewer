import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:javdb_app/services/history.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('首次记录历史不因 const [] 不可变列表而抛异常', () async {
    // list() 空时曾返回 const []，_upsert 对其 removeWhere 会抛
    // Unsupported operation，导致历史永远写不进（web 控制台 Zone error）。
    await HistoryService.recordView(
        id: 'abc', number: 'SSIS-001', title: 't', cover: '');
    final list = await HistoryService.list();
    expect(list, hasLength(1));
    expect(list.single.id, 'abc');
    expect(list.single.state, 'viewed');
  });

  test('重复浏览合并为一条且进度取较大值', () async {
    await HistoryService.recordView(id: 'abc', number: 'S-1', title: '', cover: '');
    await HistoryService.recordProgress(
        id: 'abc', number: 'S-1', title: 't', cover: 'c', seconds: 60);
    await HistoryService.recordView(id: 'abc', number: 'S-1', title: 't2', cover: 'c2');
    final list = await HistoryService.list();
    expect(list, hasLength(1));
    expect(list.single.watchSeconds, 60);
    expect(list.single.title, 't2');
  });

  test('播放超阈值后状态为看过', () async {
    await HistoryService.recordProgress(
        id: 'xyz', number: 'S-2', title: '', cover: '', seconds: kWatchedThresholdSeconds);
    final list = await HistoryService.list();
    expect(list.single.state, 'watched');
  });
}
