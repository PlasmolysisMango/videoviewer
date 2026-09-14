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

  Future<void> loadSavedCredentials() async {
    final prefs = await SharedPreferences.getInstance();
    final token = prefs.getString('jwt_token');
    final username = prefs.getString('username');
    if (token != null && username != null) {
      _client.setToken(token);
      _username = username;
      notifyListeners();
    }
  }

  Future<bool> login(String username, String password) async {
    _isLoading = true;
    _error = null;
    notifyListeners();

    try {
      AppLogger.info('Logging in as: $username');
      final result = await _client.login(username, password);
      _username = result['username'] as String?;

      // Save to shared preferences
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString('jwt_token', _client.token ?? '');
      await prefs.setString('username', _username ?? '');

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
    notifyListeners();
  }
}
