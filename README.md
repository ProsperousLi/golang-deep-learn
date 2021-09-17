* [golang\-deep\-learn](#golang-deep-learn)
     * [置顶：CIDI 从零开始](#置顶cidi-从零开始)
       * [我们如何确保某个类型实现了某个接口的所有方法呢？一般可以使用下面的方法进行检测，如果实现不完整，编译期将会报错 。](#我们如何确保某个类型实现了某个接口的所有方法呢一般可以使用下面的方法进行检测如果实现不完整编译期将会报错)
         * [string字符串高效拼接方法](#string字符串高效拼接方法)
         * [map原理](#map原理)

# golang-deep-learn

加深学习golang的工具和一些编程技巧

#### 置顶：CIDI 从零开始

详情见项目 [CIDI目录](https://github.com/ProsperousLi/golang-deep-learn/tree/main/CIDI)

#### 我们如何确保某个类型实现了某个接口的所有方法呢？一般可以使用下面的方法进行检测，如果实现不完整，编译期将会报错。

    var _ Person = (*Student)(nil)
    var _ Person = (*Worker)(nil)

#### string字符串高效拼接方法

[参考链接](https://zhuanlan.zhihu.com/p/49733937)  

- 如果是常量字符串拼接，直接使用 "+" 运算符  
  运算符是每次拼接都要申请内存，但是常量字符串的连续拼接则不算在内，由编辑器帮我们处理,如下：  

      package main  
      func main() {  
      
          addStr()  
      }  
      
      func addStr() string {  
          var s string  
          s += "123" + "456" + "789"  
          s += "aaaa"  
          s += "bbbb"  
          return s  
      }  

![image](https://github.com/ProsperousLi/golang-deep-learn/blob/main/docs/pictures/stringPlus.png)  

- 如果是动态字符串数组拼接，且使用统一拼接方法，使用strings.Join(src,split)拼接  

- 如果是字符串数组拼接，且拼接较为灵活，则使用strings.builder类型，builder.writerstring("xxxx") 拼接（推荐使用此方法，性能体现最好）  
      对于builder的拼接，在拼接之前为builder申请好内存，防止频繁的扩容。  
      

        拼接是append：  
        func (b *Builder) WriteString(s string) (int, error) {  
            b.copyCheck()  
            b.buf = append(b.buf, s...)  
            return len(s), nil  
        }  
        
        防止频繁扩容：
        var b strings.Builder
        b.Grow(cap)
        b.WriteString(xxxx)

#### map原理
