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

  /// 启动时加载已保存的凭据，如果有保存的账号密码则自动登录。
  Future<void> loadSavedCredentials() async {
    final prefs = await SharedPreferences.getInstance();
    final token = prefs.getString('jwt_token');
    final username = prefs.getString('username');

    // 有有效 token 直接恢复会话
    if (token != null && token.isNotEmpty && username != null) {
      _client.setToken(token);
      _username = username;
      notifyListeners();
      return;
    }

    // 无 token 但有保存的账号密码 → 自动登录
    final savedUser = prefs.getString('saved_user');
    final savedPass = prefs.getString('saved_pass');
    if (savedUser != null && savedUser.isNotEmpty &&
        savedPass != null && savedPass.isNotEmpty) {
      AppLogger.info('Auto-login with saved credentials for: $savedUser');
      await login(savedUser, savedPass, saveCredentials: false);
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
      _error = e.toString();
      _isLoading = false;
      notifyListeners();
      AppLogger.error('Login failed', e);
      return false;
    }
  }

  Future<void> logout() async {
    _client.setToken(null);
    _username = null;
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove('jwt_token');
    await prefs.remove('username');
    await prefs.remove('saved_user');
    await prefs.remove('saved_pass');
    notifyListeners();
  }
}
