// Basic smoke test for the VideoViewer app.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:javdb_app/main.dart';
import 'package:javdb_app/api/client.dart';

void main() {
  testWidgets('App renders login screen', (WidgetTester tester) async {
    // Create a mock client
    final client = JavDBClient('http://localhost:18888');

    // Build our app and trigger a frame.
    await tester.pumpWidget(MyApp(client: client));

    // Verify that the login screen is displayed.
    expect(find.byType(TextFormField), findsWidgets);
    expect(find.text('Login'), findsOneWidget);
  });
}
