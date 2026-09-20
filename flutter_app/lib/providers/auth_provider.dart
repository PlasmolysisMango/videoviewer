import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import '../api/client.dart';
import '../services/logger.dart';

class AuthProvider with ChangeNotifier {
  final JavDBClient _client;
  String? _username;
  bool _isLoading = false;
  String? _error;

  AuthProvider(this._client);

  String? get username => _username;
  bool get isLoading => _isLoading;
  String? get error => _error;
  bool get isLoggedIn => _client.token != null;

  /// 启动时加载已保存的凭据。
  /// 有保存的账号密码时总是重新登录刷新会话：旧 token 可能因后端重启
  /// 或过期而失效，直接恢复会导致后续接口报 “login required”。
  Future<void> loadSavedCredentials() async {
    final prefs = await SharedPreferences.getInstance();
    final username = prefs.getString('username');

    // 有保存的账号密码 → 自动登录（刷新会话）
    final savedUser = prefs.getString('saved_user');
    final savedPass = prefs.getString('saved_pass');
    if (savedUser != null && savedUser.isNotEmpty &&
        savedPass != null && savedPass.isNotEmpty) {
      AppLogger.info('Auto-login with saved credentials for: $savedUser');
      final ok = await login(savedUser, savedPass, saveCredentials: false);
      if (ok) return;
      AppLogger.warning('Auto-login failed; falling back to saved token');
    }

    // 自动登录不可用/失败时回退到旧 token（保持界面登录态）
    final token = prefs.getString('jwt_token');
    if (token != null && token.isNotEmpty && username != null) {
      _client.setToken(token);
      _username = username;
      notifyListeners();
    }
  }

  /// 登录。[saveCredentials] 为 true 时保存账号密码用于下次自动登录。
  Future<bool> login(String username, String password,
      {bool saveCredentials = true}) async {
    _isLoading = true;
    _error = null;
    notifyListeners();

    try {
      AppLogger.info('Logging in as: $username');
      final result = await _client.login(username, password);
      _username = result['username'] as String?;

      final prefs = await SharedPreferences.getInstance();
      await prefs.setString('jwt_token', _client.token ?? '');
      await prefs.setString('username', _username ?? '');

      // 保存账号密码用于自动登录
      if (saveCredentials) {
        await prefs.setString('saved_user', username);
        await prefs.setString('saved_pass', password);
      }

      _isLoading = false;
      notifyListeners();
      AppLogger.info('Login successful');
      return true;
    } catch (e) {
      _error = _friendlyLoginError(e);
      _isLoading = false;
      notifyListeners();
      AppLogger.error('Login failed', e);
      return false;
    }
  }

  /// 登录失败的用户可读提示：上游繁体错误/网络异常转成简洁中文，
  /// 避免把 Exception(...) 原文直接甩给用户。
  static String _friendlyLoginError(Object e) {
    final msg = e.toString();
    if (msg.contains('帳號') ||
        msg.contains('账号') ||
        msg.contains('密碼') ||
        msg.contains('密码') ||
        msg.contains('credential') ||
        msg.contains('401')) {
      return '用户名或密码错误，请重新输入';
    }
    if (msg.contains('SocketException') ||
        msg.contains('Connection refused') ||
        msg.contains('Failed host lookup') ||
        msg.contains('Connection closed')) {
      return '无法连接本地服务，请确认后端已启动';
    }
    // 去掉 Exception( ) 包装，其余原样展示
    final m = RegExp(r'^Exception\((.*)\)$', dotAll: true).firstMatch(msg);
    return m?.group(1) ?? msg;
  }

  /// 静默重登（登录态失效时由用户态同步自动触发）：不改动 _error/_isLoading，
  /// 不打断当前界面；成功后仅刷新 token。无保存凭据或重登失败返回 false。
  Future<bool> silentRelogin() async {
    final prefs = await SharedPreferences.getInstance();
    final savedUser = prefs.getString('saved_user');
    final savedPass = prefs.getString('saved_pass');
    if (savedUser == null || savedUser.isEmpty ||
        savedPass == null || savedPass.isEmpty) {
      return false;
    }
    try {
      AppLogger.info('Silent re-login for $savedUser');
      final result = await _client.login(savedUser, savedPass);
      _username = result['username'] as String?;
      await prefs.setString('jwt_token', _client.token ?? '');
      await prefs.setString('username', _username ?? '');
      notifyListeners();
      return true;
    } catch (e) {
      AppLogger.warning('Silent re-login failed: $e');
      return false;
    }
  }

  Future<void> logout() async {
    // 先通知后端清除会话（session.json 与凭据）。
    await _client.logout();
    _client.setToken(null);
    _username = null;
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove('jwt_token');
    await prefs.remove('username');
    await prefs.remove('saved_user');
    await prefs.remove('saved_pass');
    await prefs.remove('user_marks_want_watch');
    await prefs.remove('user_marks_watched');
    await prefs.remove('user_lists');
    notifyListeners();
  }
}
