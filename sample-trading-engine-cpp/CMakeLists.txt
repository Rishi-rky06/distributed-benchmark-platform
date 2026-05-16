cmake_minimum_required(VERSION 3.16)
project(trading_engine CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

include(FetchContent)

# Disable OpenSSL — we only need plain HTTP
set(HTTPLIB_REQUIRE_OPENSSL OFF)

FetchContent_Declare(httplib
    GIT_REPOSITORY https://github.com/yhirose/cpp-httplib.git
    GIT_TAG        v0.15.3
)
FetchContent_Declare(json
    GIT_REPOSITORY https://github.com/nlohmann/json.git
    GIT_TAG        v3.11.3
)
FetchContent_MakeAvailable(httplib json)

find_package(Threads REQUIRED)

add_executable(trading_engine src/main.cpp)
target_link_libraries(trading_engine PRIVATE
    httplib::httplib
    nlohmann_json::nlohmann_json
    Threads::Threads
)
target_compile_options(trading_engine PRIVATE -O2)
